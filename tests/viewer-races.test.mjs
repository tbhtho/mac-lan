import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import * as core from '../src/Windows/viewer-core.mjs';
const source=fs.readFileSync('src/Windows/viewer.mjs','utf8').replace(/^import[^\n]+\n/,'');
function runtime({manualDecoder=false}={}){
 const state={decoded:[],timestamps:[],creations:0,draws:0,closed:0,videoClosed:0,bitmaps:[],messages:[],focusCalls:0,decoders:[],frames:[],resizes:0,now:1};let handler,timer=0;
 const noop=()=>{},listeners=new Map(),windowListeners=new Map(),captures=new Set();
 let width=640,height=480;const animation=new Map(),timers=new Map();
 const canvas={get width(){return width},set width(v){width=v;state.resizes++},get height(){return height},set height(v){height=v;state.resizes++},style:{},focus:()=>{state.focusCalls++;sandbox.document.activeElement=canvas;},addEventListener:(type,h)=>listeners.set(type,h),getBoundingClientRect:()=>({left:0,top:0,width:640,height:480}),setPointerCapture:id=>captures.add(id),hasPointerCapture:id=>captures.has(id),releasePointerCapture:id=>captures.delete(id),getContext:()=>({clearRect:noop,drawImage:()=>state.draws++})};
 class Decoder{constructor(c){state.creations++;state.decoders.push(this);this.c=c;this.state='unconfigured';this.decodeQueueSize=0;this.chunks=[];this.listeners=new Map()}configure(){this.state='configured'}close(){this.state='closed'}addEventListener(type,h){this.listeners.set(type,h)}decode(chunk){assert.equal(this.state,'configured');state.decoded.push(chunk.type);state.timestamps.push(chunk.timestamp);this.chunks.push(chunk);this.decodeQueueSize++;if(!manualDecoder)this.complete()}complete({stale=false}={}){const chunk=this.chunks.shift();assert.ok(chunk,'a submitted encoded frame is required');this.decodeQueueSize--;const frame={timestamp:chunk.timestamp,displayWidth:640,displayHeight:480,closed:false,close(){assert.equal(this.closed,false,'each decoded frame must close exactly once');this.closed=true;state.videoClosed++}};state.frames.push(frame);if(!stale)this.listeners.get('dequeue')?.();this.c.output(frame);return frame}}
 const sandbox={...core,console,performance:{now:()=>state.now},Uint8Array,DataView,Map,Blob,atob:s=>Buffer.from(s,'base64').toString('binary'),setInterval:noop,setTimeout:(fn,ms)=>{const id=++timer;timers.set(id,{fn,at:state.now+ms});return id},clearTimeout:id=>timers.delete(id),requestAnimationFrame:fn=>{const id=++timer;animation.set(id,fn);return id},cancelAnimationFrame:id=>animation.delete(id),window:{addEventListener:(type,h)=>windowListeners.set(type,h)},document:{querySelector:s=>s==='canvas'?canvas:{textContent:''},body:{classList:{toggle:noop}}},chrome:{webview:{postMessage:m=>state.messages.push(m),addEventListener:(_,h)=>handler=h}},VideoDecoder:Decoder,EncodedVideoChunk:class{constructor(v){Object.assign(this,v)}},createImageBitmap:()=>new Promise((resolve,reject)=>state.bitmaps.push(Object.assign(()=>resolve({width:640,height:480,close:()=>state.closed++}),{reject})))};
 vm.runInContext(source,vm.createContext(sandbox));return {state,canvas,send:m=>Promise.resolve(handler({data:m})),scheduled:()=>({paints:[...animation.values()],timers:[...timers.values()].map(v=>v.fn)}),paint:()=>{const callbacks=[...animation.values()];animation.clear();for(const callback of callbacks)callback(state.now)},tick:ms=>{state.now+=ms;for(const [id,value] of [...timers])if(value.at<=state.now){timers.delete(id);value.fn()}},decodeOne:options=>state.decoders.at(-1).complete(options),pointer:(type,properties={})=>listeners.get(type)?.({clientX:320,clientY:240,pointerId:1,button:0,deltaY:5,preventDefault:noop,...properties}),blur:()=>windowListeners.get('blur')?.()};
}
function packet(kind,seq,payload){const b=Buffer.alloc(9+payload.length);b[0]=kind;b.writeUInt32BE(seq,1);b.writeUInt32BE(1000,5);Buffer.from(payload).copy(b,9);return {type:'frame',data:b.toString('base64')}}
const key=[0,0,0,1,0x67,0x42,0xc0,0x1f,0,0,0,1,0x65],delta=[0,0,0,1,0x41];
{
 const h=runtime({manualDecoder:true});await h.send({type:'video',codec:'avc1.42c01f',fps:60});
 for(let i=1;i<=10;i++)await h.send(packet(i===1?2:3,i,i===1?key:delta));
 while(h.state.decoders.at(-1).chunks.length)h.decodeOne();h.paint();
 assert.equal(h.state.decoded.length,10,'a short decoder backlog must preserve every H264 reference instead of dropping delta frames');
 assert.equal(h.state.draws,1,'decoded bursts should present only their latest picture per animation frame');
 assert.equal(h.state.videoClosed,10);assert.equal(h.state.resizes,0,'unchanged dimensions must not resize the canvas');
 console.log('PASS a short decoder backlog preserves all H264 references and presents the latest decoded picture');
}
{
 const h=runtime({manualDecoder:true});await h.send({type:'video',codec:'avc1.42c01f',fps:60,epoch:7});h.state.messages.length=0;
 await h.send({type:'frames',epoch:7,batch:3,frames:Array.from({length:8},(_,i)=>packet(i===0?2:3,i+1,i===0?key:delta).data)});
 assert.equal(h.state.decoded.length,2,'only a bounded number of chunks may be submitted to the decoder at once');
 assert.deepEqual(h.state.messages.map(m=>[m.type,m.epoch,m.batch]),[['frameAck',7,3]],'one acknowledgement covers the admitted batch');
 while(h.state.decoders.at(-1).chunks.length)h.decodeOne();h.paint();
 assert.equal(h.state.decoded.length,8);assert.equal(h.state.draws,1);assert.equal(h.state.videoClosed,8);
 assert.deepEqual(h.state.messages.filter(m=>m.type==='presented').map(m=>m.epoch),[7]);
 assert.equal(h.state.messages.filter(m=>m.type==='frameAck').length,1);
 console.log('PASS bounded native frame batches decode every reference, acknowledge once and present the active epoch');
}
{
 const h=runtime({manualDecoder:true});await h.send({type:'video',codec:'avc1.42c01f',fps:60,epoch:2});
 await h.send({type:'frames',epoch:2,batch:1,frames:[packet(2,1,key).data,packet(3,2,delta).data]});
 h.decodeOne();const old=h.state.decoders.at(-1);await h.send({type:'clear',epoch:3});const draws=h.state.draws;
 old.complete({stale:true});h.tick(1000);h.paint();
 assert.equal(h.state.draws,draws,'neither queued presentation nor stale decoder output may repaint after clear');assert.equal(h.state.videoClosed,2);
 const decoded=h.state.decoded.length;await h.send({type:'video',codec:'avc1.42c01f',epoch:2});await h.send({type:'frames',epoch:2,batch:2,frames:[packet(2,3,key).data]});
 assert.equal(h.state.decoded.length,decoded,'an older epoch must not restore an ended stream');
 assert.equal(h.state.messages.filter(m=>m.type==='presented').length,0);
 console.log('PASS clear closes queued output and ignores stale decoder callbacks, timers and older stream epochs');
}
{
 const h=runtime({manualDecoder:true});await h.send({type:'video',codec:'avc1.42c01f',fps:60});
 await h.send(packet(2,1,key));await h.send(packet(3,2,delta));await h.send(packet(3,3,delta));h.tick(251);
 const accepted=h.state.decoded.length;await h.send(packet(3,4,delta));assert.equal(h.state.decoded.length,accepted,'an expired dependency chain must wait for a new keyframe');
 await h.send(packet(2,5,key));await h.send(packet(3,6,delta));
 assert.deepEqual(h.state.decoded.slice(accepted),['key','delta'],'a fresh keyframe recovers from a bounded decoder stall');
 h.decodeOne();h.decodeOne();h.paint();assert.equal(h.state.draws,1);
 console.log('PASS an expired decoder backlog resets safely and recovers only at a fresh keyframe');
}
{
 const h=runtime({manualDecoder:true});await h.send({type:'video',codec:'jpeg',epoch:4});
 const inFlight=h.send({type:'frames',epoch:4,batch:1,frames:[packet(1,1,[0xff,0xd8,0xff,0xd9]).data,packet(1,2,[0xff,0xd8,0xff,0xd9]).data]});
 await h.send({type:'clear',epoch:5});h.state.bitmaps.shift()();for(let i=0;i<4;i++)await Promise.resolve();
 assert.equal(h.state.bitmaps.length,0,'the rest of an old asynchronous batch must not enter a new stream');await inFlight;h.paint();assert.equal(h.state.closed,1);assert.equal(h.state.draws,0);
 assert.deepEqual(h.state.messages.filter(m=>m.type==='frameAck').map(m=>[m.epoch,m.batch]),[[4,1]],'a delayed completion may only acknowledge its original identity');
 console.log('PASS a delayed JPEG batch cannot admit its remaining pictures after clear');
}
{
 const h=runtime({manualDecoder:true});await h.send({type:'video',codec:'avc1.42c01f',epoch:1});
 await h.send({type:'frames',epoch:1,batch:1,frames:[packet(2,1,key).data,packet(3,2,delta).data]});h.decodeOne();
 const stale=h.scheduled(),old=h.state.decoders.at(-1);await h.send({type:'clear',epoch:2});await h.send({type:'video',codec:'avc1.42c01f',epoch:2});
 await h.send({type:'frames',epoch:2,batch:1,frames:[packet(2,1,key).data,packet(3,2,delta).data]});h.decodeOne();
 for(const callback of [...stale.paints,...stale.timers])callback();old.c.error(new Error('old decoder'));old.listeners.get('dequeue')?.();
 h.paint();assert.equal(h.state.draws,1,'stale callbacks cannot erase the new animation request or close the new decoder');
 h.decodeOne();h.paint();assert.equal(h.state.draws,2);assert.equal(h.state.decoders.at(-1).state,'configured');
 assert.deepEqual(h.state.messages.filter(m=>m.type==='presented').map(m=>m.epoch),[2,2]);
 console.log('PASS stale presentation, timeout, decoder error and dequeue callbacks cannot disturb a new epoch');
}
{
 const h=runtime({manualDecoder:true});await h.send({type:'video',codec:'avc1.42c01f',epoch:8});
 const batch={type:'frames',epoch:8,batch:1,frames:[packet(2,1,key).data]};await h.send(batch);await h.send(batch);
 assert.equal(h.state.decoded.length,1,'a replayed batch must not decode its keyframe again');
 assert.equal(h.state.messages.filter(m=>m.type==='frameAck').length,1,'a batch identity must only be acknowledged once');
 console.log('PASS duplicate native batch identities cannot replay pictures or acknowledgements');
}
{
 const h=runtime({manualDecoder:true});await h.send({type:'video',codec:'avc1.42c01f',fps:60});
 for(let i=1;i<=66;i++){await h.send(packet(i===1?2:3,i,i===1?key:delta));const d=h.state.decoders.at(-1);if(d.state==='configured'){d.decodeQueueSize=0;d.listeners.get('dequeue')?.();}}
 assert.equal(h.state.decoded.length,64,'dequeue without output must still retain the in-flight frame-count bound');
 await h.send(packet(2,67,key));assert.equal(h.state.decoded.length,65,'a new keyframe must recover after the frame-count limit');
 console.log('PASS dequeue without output cannot grow unbounded pending decoder references');
}
{
 const h=runtime({manualDecoder:true});await h.send({type:'video',codec:'avc1.42c01f',fps:60});
 const large=Buffer.alloc(3*1024*1024);Buffer.from(key).copy(large);
 await h.send(packet(2,1,large));await h.send(packet(3,2,large));await h.send(packet(3,3,large));
 assert.equal(h.state.decoded.length,2,'encoded reference bytes must stay below the backlog memory limit');
 await h.send(packet(3,4,delta));assert.equal(h.state.decoded.length,2,'overflow must discard dependent deltas until a keyframe');
 await h.send(packet(2,5,key));assert.equal(h.state.decoded.length,3);
 console.log('PASS the encoded byte limit resets an oversized backlog and requires a fresh keyframe');
}
{
 const h=runtime({manualDecoder:true});await h.send({type:'video',codec:'jpeg',epoch:1});
 const old=h.send({type:'frames',epoch:1,batch:1,frames:[packet(1,1,[0xff,0xd8,0xff,0xd9]).data]});
 const held=h.state.bitmaps.shift();await h.send({type:'clear',epoch:2});await h.send({type:'video',codec:'avc1.42c01f',epoch:2});
 await h.send({type:'frames',epoch:2,batch:1,frames:[packet(2,1,key).data]});h.decodeOne();h.paint();const d=h.state.decoders.at(-1);
 held.reject(new Error('obsolete JPEG decode'));await old;
 assert.equal(d.state,'configured','an obsolete asynchronous JPEG rejection must not close the current decoder');
 await h.send({type:'frames',epoch:2,batch:2,frames:[packet(3,2,delta).data]});h.decodeOne();h.paint();assert.equal(h.state.draws,2);
 console.log('PASS an obsolete JPEG rejection cannot reset the current stream');
}
{
 const h=runtime();await h.send({type:'video',codec:'jpeg',epoch:1});
 const old=h.send({type:'frames',epoch:1,batch:1,frames:[packet(1,1,[0xff,0xd8,0xff,0xd9]).data]});
 for(let epoch=2;epoch<=10;epoch++){
  await h.send({type:'clear',epoch});await h.send({type:'video',codec:'jpeg',epoch});
  const next=h.send({type:'frames',epoch,batch:1,frames:[packet(1,1,[0xff,0xd8,0xff,0xd9]).data]});for(let i=0;i<3;i++)await Promise.resolve();
  assert.equal(h.state.bitmaps.length,1,'clear must not create another noncancelable bitmap decode while one is in flight');await next;
 }
 h.state.bitmaps.shift()();await old;assert.equal(h.state.bitmaps.length,1,'the latest matching JPEG resumes after the one global decode settles');h.state.bitmaps.shift()();for(let i=0;i<4;i++)await Promise.resolve();h.paint();assert.equal(h.state.closed,2);assert.equal(h.state.draws,1);assert.equal(h.state.messages.filter(m=>m.type==='presented').at(-1).epoch,10);
 console.log('PASS repeated stream resets keep global asynchronous JPEG work bounded');
}
{
 const h=runtime();await h.send({type:'clear',epoch:1});
 assert.deepEqual(h.state.messages.filter(m=>m.type==='clearAck').map(m=>m.epoch),[1],'clear acknowledges its matching epoch only after reset');
 await h.send({type:'armed',active:true,epoch:1});await h.send({type:'clear',epoch:2});await h.send({type:'armed',active:true,epoch:1});h.state.messages.length=0;
 h.pointer('pointermove');assert.equal(h.state.messages.length,0,'stale armed status cannot reactivate an ended stream');
 h.pointer('pointerdown');h.pointer('pointerup');assert.equal(h.state.messages.at(-1).epoch,2,'a new explicit activation is bound to the current stream');
 await h.send({type:'armed',active:true,epoch:2});h.pointer('pointermove');h.pointer('pointerdown');h.pointer('pointerup');h.pointer('wheel');h.blur();
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl','move','move','button','button','scroll','blur']);
 assert.ok(h.state.messages.every(m=>m.epoch===2),'all pointer, activation and release messages carry their stream epoch');
 console.log('PASS clear handshake and input epochs prevent stale armed status or pointer messages crossing streams');
}
{
 const h=runtime({manualDecoder:true});await h.send({type:'video',codec:'avc1.42c01f'});await h.send(packet(2,1,key));h.state.now+=300;h.decodeOne({stale:true});h.paint();
 assert.equal(h.state.draws,0,'a delayed decoder output must not refresh a stale picture before its timeout callback runs');assert.equal(h.state.videoClosed,1);
 const accepted=h.state.decoded.length;await h.send(packet(3,2,delta));assert.equal(h.state.decoded.length,accepted,'an overage decode resets its dependent reference chain');
 await h.send(packet(2,3,key));h.decodeOne();h.paint();assert.equal(h.state.draws,1);
 console.log('PASS an overage decode cannot paint or refresh freshness when its timeout task is delayed');
}
{
 const h=runtime();await h.send({type:'video',codec:'jpeg'});const old=h.send(packet(1,1,[0xff,0xd8,0xff,0xd9]));h.state.now+=300;h.state.bitmaps.shift()();await old;h.paint();
 assert.equal(h.state.draws,0,'an overage JPEG decode must close without refreshing the displayed picture');assert.equal(h.state.closed,1);
 await h.send({type:'video',codec:'avc1.42c01f'});await h.send(packet(2,1,key));h.state.now+=300;h.paint();
 assert.equal(h.state.draws,0,'an overdue animation callback must not present an old decoded picture');assert.equal(h.state.videoClosed,1);
 console.log('PASS delayed JPEG completion and animation callbacks cannot advertise stale pixels as fresh');
}
{
 const h=runtime(),c={type:'video',codec:'avc1.42c01f',fps:60};
 await h.send(c);await h.send(packet(2,1,[0,0,0,1,0x67,0x42,0xc0,0x1f,0,0,0,1,0x65]));await h.send(c);await h.send(packet(3,2,[0,0,0,1,0x41]));
 assert.deepEqual(h.state.decoded,['key','delta']);assert.deepEqual(h.state.timestamps,[16667,33333],'60fps frames must not retain 30fps decoder timestamps');assert.equal(h.state.creations,1,'unchanged keyframes must reuse decoder');console.log('PASS identical H264 metadata after keyframe preserves delta at 60fps');
}
{
 const h=runtime();await h.send({type:'video',codec:'jpeg'});
 const pending=h.send(packet(1,1,[0xff,0xd8,0xff,0xd9]));assert.equal(h.state.bitmaps.length,1);
 await h.send({type:'clear'});const draws=h.state.draws;h.state.bitmaps.shift()();await pending;
 assert.equal(h.state.draws,draws,'stale JPEG painted after clear');assert.equal(h.state.closed,1);console.log('PASS asynchronous JPEG cannot repaint after clear');
}
{
 const h=runtime();h.state.messages.length=0;
 h.pointer('pointerdown',{button:2});h.pointer('pointerdown',{clientX:-1});
 assert.equal(h.state.messages.length,0,'only a left click inside the picture may request control');
 h.pointer('pointerdown');
 assert.equal(h.state.messages.length,0,'control must not re-focus native WebView2 while the activation press holds browser capture');
 h.pointer('pointermove');h.pointer('wheel');h.pointer('pointerup');
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl'],'the complete activation click requests control without clicking through');
 await h.send({type:'armed',active:true});
 h.pointer('pointerdown');h.pointer('pointerup');
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl','move','button','button']);
 assert.deepEqual(h.state.messages.filter(m=>m.type==='button').map(m=>m.down),[true,false]);
 console.log('PASS first desktop click requests control without clicking through; later clicks route');
}
{
 for(const [reason,cancel] of [['pointer-cancel',h=>h.pointer('pointercancel')],['viewer-blur',h=>h.blur()]]){
  for(const requested of [false,true]){
   const h=runtime();h.state.messages.length=0;h.pointer('pointerdown');if(requested)h.pointer('pointerup');cancel(h);
   assert.deepEqual(h.state.messages.map(m=>m.type),requested?['requestControl','blur']:['blur'],'cancel revokes a press or an asynchronous control request');
   assert.equal(h.state.messages.at(-1).reason,reason,'the native owner receives a fixed diagnostic release reason');
   h.pointer('pointermove');h.pointer('wheel');assert.equal(h.state.messages.length,requested?2:1,'cancellation suppresses further input locally');
   await h.send({type:'armed',active:false});h.state.messages.length=0;h.pointer('pointerdown');h.pointer('pointerup');await h.send({type:'armed',active:true});h.pointer('pointerdown');h.pointer('pointerup');
   assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl','move','button','button'],'a new explicit click is required after cancellation');
  }
 }
 console.log('PASS pointer cancel and blur revoke pending/granted activation and require a new click');
}
{
 const h=runtime();h.state.messages.length=0;h.pointer('pointerdown');h.pointer('lostpointercapture');h.pointer('pointerup');
 assert.deepEqual(h.state.messages.map(m=>m.type),['blur'],'unexpected capture loss cancels an unfinished activation press');
 h.state.messages.length=0;h.pointer('pointerdown');h.pointer('pointerup');h.pointer('lostpointercapture');await h.send({type:'armed',active:true});
 const focusCalls=h.state.focusCalls;for(let i=0;i<5;i++)await h.send({type:'armed',active:true});
 assert.equal(h.state.focusCalls,focusCalls,'periodic control state messages must not refocus an already focused canvas');
 h.pointer('pointerdown');h.pointer('pointerup');
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl','move','button','button'],'normal capture release after a committed click cannot revoke control');
 console.log('PASS release capture before activation and repeated control status preserve active input');
}
{
 const h=runtime();h.state.messages.length=0;h.pointer('pointerdown');h.pointer('pointerup',{clientX:-1});
 assert.deepEqual(h.state.messages,[],'dragging an activation press out of the picture must not grant control');
 h.pointer('pointerdown');h.pointer('pointerup');await h.send({type:'armed',active:true});h.blur();
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl','blur'],'focus loss after activation still releases control');
 console.log('PASS an activation drag outside the picture cancels; focus loss still releases granted control');
}
{
 const h=runtime();h.state.messages.length=0;h.pointer('pointerdown');h.pointer('pointerup');await h.send({type:'armed',active:true});h.pointer('pointercancel');
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl','blur'],'pointer cancellation still revokes granted control');
 h.pointer('pointermove');h.pointer('wheel');assert.equal(h.state.messages.length,2);
 console.log('PASS pointer cancellation releases granted control and suppresses further input');
}
{
 const h=runtime();h.state.messages.length=0;h.pointer('pointerdown');await h.send({type:'clear'});await h.send({type:'armed',active:false});
 h.pointer('pointerdown');h.pointer('pointerup');await h.send({type:'armed',active:true});h.pointer('pointerdown');h.pointer('pointerup');
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl','move','button','button'],'clear resets the activation gesture');
 console.log('PASS stream clearing cannot leave activation stuck');
}
{
 const h=runtime();h.canvas.width=1920;h.canvas.height=1080;await h.send({type:'video',codec:'jpeg',presentation:'fit'});h.state.messages.length=0;
 assert.equal(h.canvas.style.objectFit,'contain');h.pointer('pointerdown',{clientY:10});
 assert.equal(h.state.messages.length,0,'App Priority letterboxes cannot activate control');
 await h.send({type:'video',codec:'jpeg',presentation:'fill'});assert.equal(h.canvas.style.objectFit,'fill');h.pointer('pointerdown',{clientY:10});h.pointer('pointerup',{clientY:10});
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl'],'the full desktop fills the picture and accepts clicks there');
 console.log('PASS App Priority preserves aspect fit; full desktop fills and uses matching pointer mapping');
}
