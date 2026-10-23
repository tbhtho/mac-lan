import {parseFrame,SequenceGate,mapPointer,videoFrameDuration} from './viewer-core.mjs';
const canvas=document.querySelector('canvas'),ctx=canvas.getContext('2d',{alpha:false,desynchronized:true}),hint=document.querySelector('#hint');
const post=m=>chrome.webview.postMessage(m);
let decoder,configuredCodec,config,sequence=new SequenceGate(),armed=false,drawn=0,encode=0,lastDecode=0,started=performance.now(),pending=new Map(),jpegBusy=false,serial=0,renderGeneration=0;
let frameDuration=videoFrameDuration({fps:60}),activationPointer=null,presentation='fill';
function show(text){hint.textContent=text;}
function reset(){renderGeneration++;if(decoder){decoder.close();decoder=null;}sequence.reset();pending.clear();ctx.clearRect(0,0,canvas.width,canvas.height);drawn=0;}
function newDecoder(){
  if(decoder)decoder.close();pending.clear();
  const generation=renderGeneration;
  decoder=new VideoDecoder({output:frame=>{if(generation!==renderGeneration){frame.close();return;}let t=pending.get(frame.timestamp);pending.delete(frame.timestamp);canvas.width=frame.displayWidth;canvas.height=frame.displayHeight;ctx.drawImage(frame,0,0);frame.close();drawn++;post({type:"presented"});if(t!==undefined)lastDecode=performance.now()-t;show(armed?'CONTROLLING MAC � Ctrl+Alt+Shift+Backspace releases':'VIEW ONLY � click Control Mac to enable input');},error:e=>{sequence.reset();show('Decoder reset; waiting for keyframe: '+e.message);}});
  decoder.configure(config);configuredCodec=config.codec;
}
chrome.webview.addEventListener('message',async e=>{
  const m=e.data;
  if(m.type==='armed'){armed=m.active;if(armed)canvas.focus();document.body.classList.toggle('armed',armed);show(armed?'CONTROLLING MAC � escape Ctrl+Alt+Shift+Backspace':'VIEW ONLY � control released');return;}
  if(m.type==='clear'){activationPointer=null;reset();show('VIEW ONLY � stream paused or disconnected');return;}
  if(m.type==='video'){
    presentation=m.presentation==='fit'?'fit':'fill';canvas.style.objectFit=presentation==='fit'?'contain':'fill';
    frameDuration=videoFrameDuration(m);
    if(m.codec==='jpeg'){if(config){reset();config=null;}return;}
    // Key packets carry SPS/PPS and configure themselves too; pipe delivery order is irrelevant.
    if(config?.codec===m.codec)return;
    config={codec:m.codec,hardwareAcceleration:'prefer-hardware',optimizeForLatency:true};
    return;
  }
  if(m.type!=='frame')return;
  try{
    const b=Uint8Array.from(atob(m.data),c=>c.charCodeAt(0)),p=parseFrame(b);encode=p.encodeMs;
    if(p.kind===1){if(jpegBusy)return;jpegBusy=true;try{let t=performance.now(),generation=renderGeneration;const image=await createImageBitmap(new Blob([p.data],{type:'image/jpeg'}));if(generation!==renderGeneration){image.close();return;}canvas.width=image.width;canvas.height=image.height;ctx.drawImage(image,0,0);image.close();lastDecode=performance.now()-t;drawn++;post({type:"presented"});show(armed?'CONTROLLING MAC � escape Ctrl+Alt+Shift+Backspace':'VIEW ONLY � JPEG diagnostic');}finally{jpegBusy=false;}return;}
    if(p.key){const s=p.data;if(s.length>=8&&s[0]===0&&s[1]===0&&s[2]===0&&s[3]===1&&(s[4]&31)===7){const hex=n=>n.toString(16).padStart(2,'0');config={codec:'avc1.'+hex(s[5])+hex(s[6])+hex(s[7]),hardwareAcceleration:'prefer-hardware',optimizeForLatency:true};}}
    if(!config||!sequence.accept(p.sequence,p.key))return;
    if(!decoder||decoder.state!=='configured'||configuredCodec!==config.codec){if(!p.key)return;newDecoder();}
    if(decoder.decodeQueueSize>=2){sequence.reset();return;}

    const timestamp=Math.round(++serial*frameDuration);pending.set(timestamp,performance.now());
    decoder.decode(new EncodedVideoChunk({type:p.key?'key':'delta',timestamp,data:p.data}));
  }catch(err){sequence.reset();show('Video error: '+err.message);}
  finally{post({type:'frameAck'});}
});
setInterval(()=>{let secs=(performance.now()-started)/1000;post({type:'stats',message:`${(drawn/Math.max(secs,.1)).toFixed(1)} fps � encode ${encode.toFixed(1)} ms � local decode/submit ${lastDecode.toFixed(1)} ms � ${config?'H.264 (hardware preferred)':'JPEG'}`});drawn=0;started=performance.now();},1000);
function coords(e){const r=canvas.getBoundingClientRect();return mapPointer(e.clientX-r.left,e.clientY-r.top,r.width,r.height,canvas.width,canvas.height,presentation);}
canvas.addEventListener('pointermove',e=>{if(!armed||activationPointer!==null)return;const p=coords(e);if(p)post({type:'move',...p});});
canvas.addEventListener('pointerdown',e=>{
  canvas.focus();const p=coords(e);if(!p||activationPointer!==null)return;
  if(!armed){if(e.button!==0)return;e.preventDefault();activationPointer=e.pointerId;canvas.setPointerCapture(e.pointerId);post({type:'requestControl'});return;}
  e.preventDefault();canvas.setPointerCapture(e.pointerId);post({type:'move',...p});post({type:'button',button:e.button,down:true});
});
canvas.addEventListener('pointerup',e=>{
  if(activationPointer===e.pointerId){activationPointer=null;e.preventDefault();}
  else if(armed&&activationPointer===null){e.preventDefault();post({type:'button',button:e.button,down:false});}
  if(canvas.hasPointerCapture(e.pointerId))canvas.releasePointerCapture(e.pointerId);
});
function releaseControl(){const notify=armed||activationPointer!==null;activationPointer=null;armed=false;if(notify)post({type:'blur'});}
canvas.addEventListener('pointercancel',e=>{if(activationPointer===e.pointerId||armed)releaseControl();if(canvas.hasPointerCapture(e.pointerId))canvas.releasePointerCapture(e.pointerId);});
canvas.addEventListener('lostpointercapture',e=>{if(activationPointer===e.pointerId)releaseControl();});
canvas.addEventListener('wheel',e=>{if(armed&&activationPointer===null){e.preventDefault();post({type:'scroll',delta:Math.max(-2400,Math.min(2400,Math.round(-e.deltaY)))});}},{passive:false});
canvas.addEventListener('contextmenu',e=>e.preventDefault());
post({type:'loaded'});
window.addEventListener('blur',releaseControl);
