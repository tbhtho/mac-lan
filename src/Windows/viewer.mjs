import {parseFrame,SequenceGate,mapPointer,videoFrameDuration} from './viewer-core.mjs';
const canvas=document.querySelector('canvas'),ctx=canvas.getContext('2d',{alpha:false,desynchronized:true}),hint=document.querySelector('#hint');
const post=m=>chrome.webview.postMessage(m);
let decoder,configuredCodec,config,sequence=new SequenceGate(),armed=false,drawn=0,encode=0,lastDecode=0,started=performance.now(),pending=new Map(),jpegBusy=false,serial=0,renderGeneration=0,streamEpoch=0,lastBatch=0;
let frameDuration=videoFrameDuration({fps:60}),activationPointer=null,controlRequested=false;
const MAX_BATCH_FRAMES=8,MAX_BATCH_BYTES=4*1024*1024,MAX_BUFFERED_FRAMES=64,MAX_BUFFERED_BYTES=8*1024*1024,MAX_BUFFERED_MS=250;
let encoded=[],bufferedBytes=0,pumping=false,queueTimer=null,picture=null,paintRequest=null,queuedJpeg=null;
const input=m=>post({...m,epoch:streamEpoch});
function show(text){hint.textContent=text;}
function reset(clearPicture=true){
  renderGeneration++;if(queueTimer!==null){clearTimeout(queueTimer);queueTimer=null;}if(paintRequest!==null){cancelAnimationFrame(paintRequest);paintRequest=null;}
  if(picture){picture.frame.close();picture=null;}if(decoder){try{decoder.close();}catch{}decoder=null;}
  configuredCodec=null;sequence.reset();pending.clear();encoded=[];bufferedBytes=0;queuedJpeg=null;
  if(clearPicture){ctx.clearRect(0,0,canvas.width,canvas.height);drawn=0;}
}
function recover(message){reset(false);show(message);}
function epochFor(m,advance=false){
  if(m.epoch===undefined)return streamEpoch===0;
  if(!Number.isSafeInteger(m.epoch)||m.epoch<=0||m.epoch<streamEpoch)return false;
  if(m.epoch>streamEpoch){if(!advance)return false;reset();config=null;activationPointer=null;controlRequested=false;armed=false;document.body.classList.toggle('armed',false);streamEpoch=m.epoch;lastBatch=0;}
  return true;
}
function oldestBuffered(){return Math.min(pending.values().next().value?.queuedAt??Infinity,encoded[0]?.queuedAt??Infinity);}
function watchQueue(){
  if(queueTimer!==null){clearTimeout(queueTimer);queueTimer=null;}
  const oldest=oldestBuffered();if(!Number.isFinite(oldest))return;
  const generation=renderGeneration;
  queueTimer=setTimeout(()=>{if(generation!==renderGeneration)return;queueTimer=null;if(performance.now()-oldestBuffered()>=MAX_BUFFERED_MS)recover('Decoder delayed; waiting for a fresh keyframe');else watchQueue();},Math.max(1,MAX_BUFFERED_MS-(performance.now()-oldest)));
}
function present(frame,queuedAt){
  if(picture)picture.frame.close();picture={frame,queuedAt};if(paintRequest!==null)return;
  const generation=renderGeneration,epoch=streamEpoch;
  paintRequest=requestAnimationFrame(()=>{
    if(generation!==renderGeneration)return;paintRequest=null;const queued=picture;picture=null;if(!queued)return;const latest=queued.frame;
    try{
      if(performance.now()-queued.queuedAt>=MAX_BUFFERED_MS)return;
      const width=latest.displayWidth??latest.width,height=latest.displayHeight??latest.height;
      if(canvas.width!==width)canvas.width=width;if(canvas.height!==height)canvas.height=height;
      ctx.drawImage(latest,0,0);drawn++;post({type:'presented',epoch});
      show(armed?'CONTROLLING MAC - Ctrl+Alt+Shift+Backspace releases':'VIEW ONLY - click to control Mac');
    }finally{latest.close();}
  });
}
function pump(){
  if(pumping||!decoder||decoder.state!=='configured')return;
  if(performance.now()-oldestBuffered()>=MAX_BUFFERED_MS){recover('Decoder delayed; waiting for a fresh keyframe');return;}
  pumping=true;
  try{
    while(encoded.length&&decoder?.state==='configured'&&decoder.decodeQueueSize<2){
      const next=encoded.shift();pending.set(next.timestamp,next);
      decoder.decode(new EncodedVideoChunk({type:next.key?'key':'delta',timestamp:next.timestamp,data:next.data}));
    }
  }catch(err){recover('Decoder reset; waiting for keyframe: '+err.message);}
  finally{pumping=false;watchQueue();}
}
function newDecoder(){
  const generation=renderGeneration;
  const created=new VideoDecoder({output:frame=>{
    if(generation!==renderGeneration||created!==decoder){frame.close();return;}
    const submitted=pending.get(frame.timestamp);if(!submitted){frame.close();return;}
    if(performance.now()-submitted.queuedAt>=MAX_BUFFERED_MS){frame.close();recover('Decoder delayed; waiting for a fresh keyframe');return;}
    pending.delete(frame.timestamp);bufferedBytes-=submitted.data.byteLength;lastDecode=performance.now()-submitted.queuedAt;
    present(frame,submitted.queuedAt);pump();watchQueue();
  },error:e=>{if(generation===renderGeneration&&created===decoder)recover('Decoder reset; waiting for keyframe: '+e.message);}});
  decoder=created;decoder.configure(config);configuredCodec=config.codec;
  decoder.addEventListener('dequeue',()=>{if(generation===renderGeneration&&created===decoder)pump();});
}
async function receivePacket(p,queuedAt=performance.now()){
  encode=p.encodeMs;
  if(p.kind===1){
    if(performance.now()-queuedAt>=MAX_BUFFERED_MS)return;
    if(jpegBusy){queuedJpeg={packet:p,queuedAt,generation:renderGeneration,epoch:streamEpoch};return;}jpegBusy=true;const generation=renderGeneration,epoch=streamEpoch;
    try{const image=await createImageBitmap(new Blob([p.data],{type:'image/jpeg'}));if(generation!==renderGeneration||epoch!==streamEpoch||performance.now()-queuedAt>=MAX_BUFFERED_MS){image.close();return;}lastDecode=performance.now()-queuedAt;present(image,queuedAt);}
    catch(err){if(generation===renderGeneration&&epoch===streamEpoch)recover('Video error: '+err.message);}
    finally{jpegBusy=false;const next=queuedJpeg;queuedJpeg=null;if(next&&next.generation===renderGeneration&&next.epoch===streamEpoch)void receivePacket(next.packet,next.queuedAt);}return;
  }
  if(p.key){const s=p.data;if(s.length>=8&&s[0]===0&&s[1]===0&&s[2]===0&&s[3]===1&&(s[4]&31)===7){const hex=n=>n.toString(16).padStart(2,'0');config={codec:'avc1.'+hex(s[5])+hex(s[6])+hex(s[7]),hardwareAcceleration:'prefer-hardware',optimizeForLatency:true};}}
  if(!config)return;
  // A decoder may fall behind briefly, but every delta still depends on the
  // earlier encoded pictures. Keep those references in order until dequeue.
  if(performance.now()-oldestBuffered()>=MAX_BUFFERED_MS||encoded.length+pending.size>=MAX_BUFFERED_FRAMES||bufferedBytes+p.data.byteLength>MAX_BUFFERED_BYTES){recover('Decoder delayed; waiting for a fresh keyframe');if(!p.key)return;}
  if(!sequence.accept(p.sequence,p.key)){recover('Video sequence changed; waiting for a fresh keyframe');return;}
  if(!decoder||decoder.state!=='configured'||configuredCodec!==config.codec){
    if(!p.key)return;if(decoder){reset(false);sequence.accept(p.sequence,true);}newDecoder();
  }
  const timestamp=Math.round(++serial*frameDuration);encoded.push({timestamp,key:p.key,data:p.data,queuedAt:performance.now()});bufferedBytes+=p.data.byteLength;pump();watchQueue();
}
chrome.webview.addEventListener('message',async e=>{
  const m=e.data;
  if(m.type==='armed'){if(!epochFor(m))return;const next=m.active===true;if(next&&!armed&&document.activeElement!==canvas)canvas.focus();armed=next;controlRequested=false;document.body.classList.toggle('armed',armed);show(armed?'CONTROLLING MAC � escape Ctrl+Alt+Shift+Backspace':'VIEW ONLY � control released');return;}
  if(m.type==='clear'){if(!epochFor(m,true))return;activationPointer=null;controlRequested=false;armed=false;document.body.classList.toggle('armed',false);reset();config=null;show('VIEW ONLY - stream paused or disconnected');if(m.epoch!==undefined)post({type:'clearAck',epoch:streamEpoch});return;}
  if(m.type==='video'){
    if(!epochFor(m,true))return;
    // Older desktop hosts advertise fill; always preserve the received picture's proportions.
    canvas.style.objectFit='contain';
    frameDuration=videoFrameDuration(m);
    if(m.codec==='jpeg'){if(config){reset();config=null;}return;}
    // Key packets carry SPS/PPS and configure themselves too; pipe delivery order is irrelevant.
    if(config?.codec===m.codec)return;
    if(jpegBusy||queuedJpeg)reset(false);config={codec:m.codec,hardwareAcceleration:'prefer-hardware',optimizeForLatency:true};
    return;
  }
  if(m.type!=='frame'&&m.type!=='frames')return;
  if(!epochFor(m))return;
  const batch=m.type==='frames';
  if(batch){if(!Number.isSafeInteger(m.batch)||m.batch<=lastBatch)return;lastBatch=m.batch;}
  const epoch=streamEpoch;let generation=renderGeneration;
  try{
    const values=batch?m.frames:[m.data];if(!Array.isArray(values)||values.length<1||values.length>MAX_BATCH_FRAMES)throw Error('Invalid frame batch');
    let bytes=0;const packets=values.map(value=>{if(typeof value!=='string'||value.length>Math.ceil(MAX_BATCH_BYTES/3)*4)throw Error('Invalid frame size');const b=Uint8Array.from(atob(value),c=>c.charCodeAt(0));bytes+=b.byteLength;if(bytes>MAX_BATCH_BYTES)throw Error('Frame batch exceeds its byte limit');return parseFrame(b);});
    for(const p of packets){const operation=receivePacket(p);generation=renderGeneration;await operation;if(generation!==renderGeneration||epoch!==streamEpoch)break;}
  }catch(err){if(generation===renderGeneration&&epoch===streamEpoch)recover('Video error: '+err.message);}
  finally{post(batch?{type:'frameAck',epoch:m.epoch,batch:m.batch}:{type:'frameAck'});}
});
setInterval(()=>{let secs=(performance.now()-started)/1000;post({type:'stats',message:`${(drawn/Math.max(secs,.1)).toFixed(1)} fps � encode ${encode.toFixed(1)} ms � local decode/submit ${lastDecode.toFixed(1)} ms � ${config?'H.264 (hardware preferred)':'JPEG'}`});drawn=0;started=performance.now();},1000);
function coords(e){const r=canvas.getBoundingClientRect();return mapPointer(e.clientX-r.left,e.clientY-r.top,r.width,r.height,canvas.width,canvas.height);}
canvas.addEventListener('pointermove',e=>{if(!armed||activationPointer!==null)return;const p=coords(e);if(p)input({type:'move',...p});});
canvas.addEventListener('pointerdown',e=>{
  const p=coords(e);if(!p){e.preventDefault();return;}if(activationPointer!==null||controlRequested)return;if(document.activeElement!==canvas)canvas.focus();
  if(!armed){if(e.button!==0)return;e.preventDefault();activationPointer=e.pointerId;canvas.setPointerCapture(e.pointerId);return;}
  e.preventDefault();canvas.setPointerCapture(e.pointerId);input({type:'move',...p});input({type:'button',button:e.button,down:true});
});
canvas.addEventListener('pointerup',e=>{
  const activate=activationPointer===e.pointerId;
  if(activate){activationPointer=null;e.preventDefault();}
  else if(armed&&activationPointer===null&&canvas.hasPointerCapture(e.pointerId)){e.preventDefault();input({type:'button',button:e.button,down:false});}
  if(canvas.hasPointerCapture(e.pointerId))canvas.releasePointerCapture(e.pointerId);
  // Commit the consumed click after capture is released. Native focus changes
  // during pointerdown can otherwise cancel that same activation gesture.
  if(activate&&coords(e)){controlRequested=true;input({type:'requestControl'});}
});
function releaseControl(reason){const notify=armed||activationPointer!==null||controlRequested;activationPointer=null;controlRequested=false;armed=false;if(notify)input({type:'blur',reason});}
canvas.addEventListener('pointercancel',e=>{if(activationPointer===e.pointerId||armed||controlRequested)releaseControl('pointer-cancel');if(canvas.hasPointerCapture(e.pointerId))canvas.releasePointerCapture(e.pointerId);});
canvas.addEventListener('lostpointercapture',e=>{if(activationPointer===e.pointerId)releaseControl('pointer-cancel');});
canvas.addEventListener('wheel',e=>{if(armed&&activationPointer===null&&coords(e)){e.preventDefault();input({type:'scroll',delta:Math.max(-2400,Math.min(2400,Math.round(-e.deltaY)))});}},{passive:false});
canvas.addEventListener('contextmenu',e=>e.preventDefault());
post({type:'loaded'});
window.addEventListener('blur',()=>releaseControl('viewer-blur'));
