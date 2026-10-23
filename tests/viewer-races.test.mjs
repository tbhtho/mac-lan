import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import * as core from '../src/Windows/viewer-core.mjs';
const source=fs.readFileSync('src/Windows/viewer.mjs','utf8').replace(/^import[^\n]+\n/,'');
function runtime(){
 const state={decoded:[],timestamps:[],creations:0,draws:0,closed:0,bitmaps:[],messages:[]};let handler;
 const noop=()=>{},listeners=new Map(),windowListeners=new Map(),captures=new Set();
 const canvas={width:640,height:480,style:{},focus:noop,addEventListener:(type,h)=>listeners.set(type,h),getBoundingClientRect:()=>({left:0,top:0,width:640,height:480}),setPointerCapture:id=>captures.add(id),hasPointerCapture:id=>captures.has(id),releasePointerCapture:id=>captures.delete(id),getContext:()=>({clearRect:noop,drawImage:()=>state.draws++})};
 class Decoder{constructor(c){state.creations++;this.c=c;this.state='unconfigured';this.decodeQueueSize=0}configure(){this.state='configured'}close(){this.state='closed'}decode(chunk){assert.equal(this.state,'configured');state.decoded.push(chunk.type);state.timestamps.push(chunk.timestamp);this.c.output({timestamp:chunk.timestamp,displayWidth:640,displayHeight:480,close:noop})}}
 const sandbox={...core,console,performance:{now:()=>1},Uint8Array,DataView,Map,Blob,atob:s=>Buffer.from(s,'base64').toString('binary'),setInterval:noop,window:{addEventListener:(type,h)=>windowListeners.set(type,h)},document:{querySelector:s=>s==='canvas'?canvas:{textContent:''},body:{classList:{toggle:noop}}},chrome:{webview:{postMessage:m=>state.messages.push(m),addEventListener:(_,h)=>handler=h}},VideoDecoder:Decoder,EncodedVideoChunk:class{constructor(v){Object.assign(this,v)}},createImageBitmap:()=>new Promise(resolve=>state.bitmaps.push(()=>resolve({width:640,height:480,close:()=>state.closed++})))};
 vm.runInContext(source,vm.createContext(sandbox));return {state,canvas,send:m=>Promise.resolve(handler({data:m})),pointer:(type,properties={})=>listeners.get(type)?.({clientX:320,clientY:240,pointerId:1,button:0,deltaY:5,preventDefault:noop,...properties}),blur:()=>windowListeners.get('blur')?.()};
}
function packet(kind,seq,payload){const b=Buffer.alloc(9+payload.length);b[0]=kind;b.writeUInt32BE(seq,1);b.writeUInt32BE(1000,5);Buffer.from(payload).copy(b,9);return {type:'frame',data:b.toString('base64')}}
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
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl'],'a view-only click explicitly requests control');
 await h.send({type:'armed',active:true});
 h.pointer('pointermove');h.pointer('wheel');h.pointer('pointerup');
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl'],'the activation press and release must not click through after asynchronous arming');
 h.pointer('pointerdown');h.pointer('pointerup');
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl','move','button','button']);
 assert.deepEqual(h.state.messages.filter(m=>m.type==='button').map(m=>m.down),[true,false]);
 console.log('PASS first desktop click requests control without clicking through; later clicks route');
}
{
 for(const cancel of [h=>h.pointer('pointercancel'),h=>h.blur(),h=>h.pointer('lostpointercapture')]){
  for(const granted of [false,true]){
   const h=runtime();h.state.messages.length=0;h.pointer('pointerdown');if(granted)await h.send({type:'armed',active:true});cancel(h);
   assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl','blur'],'cancel revokes pending and already granted activation');
   h.pointer('pointermove');h.pointer('wheel');assert.equal(h.state.messages.length,2,'cancellation suppresses further input locally');
   await h.send({type:'armed',active:false});h.pointer('pointerdown');await h.send({type:'armed',active:true});h.pointer('pointerup');h.pointer('pointerdown');h.pointer('pointerup');
   assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl','blur','requestControl','move','button','button'],'a new explicit click is required after cancellation');
  }
 }
 console.log('PASS pointer cancel, blur and capture loss revoke pending/granted activation and require a new click');
}
{
 const h=runtime();h.state.messages.length=0;h.pointer('pointerdown');await h.send({type:'clear'});await h.send({type:'armed',active:false});
 h.pointer('pointerdown');await h.send({type:'armed',active:true});h.pointer('pointerup');h.pointer('pointerdown');h.pointer('pointerup');
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl','requestControl','move','button','button'],'clear resets the activation gesture');
 console.log('PASS stream clearing cannot leave activation stuck');
}
{
 const h=runtime();h.canvas.width=1920;h.canvas.height=1080;await h.send({type:'video',codec:'jpeg',presentation:'fit'});h.state.messages.length=0;
 assert.equal(h.canvas.style.objectFit,'contain');h.pointer('pointerdown',{clientY:10});
 assert.equal(h.state.messages.length,0,'App Priority letterboxes cannot activate control');
 await h.send({type:'video',codec:'jpeg',presentation:'fill'});assert.equal(h.canvas.style.objectFit,'fill');h.pointer('pointerdown',{clientY:10});
 assert.deepEqual(h.state.messages.map(m=>m.type),['requestControl'],'the full desktop fills the picture and accepts clicks there');
 console.log('PASS App Priority preserves aspect fit; full desktop fills and uses matching pointer mapping');
}
