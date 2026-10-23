import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import * as core from '../src/Windows/viewer-core.mjs';
const source=fs.readFileSync('src/Windows/viewer.mjs','utf8').replace(/^import[^\n]+\n/,'');
function runtime(){
 const state={decoded:[],draws:0,closed:0,bitmaps:[],messages:[]};let handler;
 const noop=()=>{};
 const canvas={width:640,height:480,focus:noop,addEventListener:noop,getBoundingClientRect:()=>({left:0,top:0,width:640,height:480}),setPointerCapture:noop,hasPointerCapture:()=>false,releasePointerCapture:noop,getContext:()=>({clearRect:noop,drawImage:()=>state.draws++})};
 class Decoder{constructor(c){this.c=c;this.state='unconfigured';this.decodeQueueSize=0}configure(){this.state='configured'}close(){this.state='closed'}decode(chunk){assert.equal(this.state,'configured');state.decoded.push(chunk.type);this.c.output({timestamp:chunk.timestamp,displayWidth:640,displayHeight:480,close:noop})}}
 const sandbox={...core,console,performance:{now:()=>1},Uint8Array,DataView,Map,Blob,atob:s=>Buffer.from(s,'base64').toString('binary'),setInterval:noop,window:{addEventListener:noop},document:{querySelector:s=>s==='canvas'?canvas:{textContent:''},body:{classList:{toggle:noop}}},chrome:{webview:{postMessage:m=>state.messages.push(m),addEventListener:(_,h)=>handler=h}},VideoDecoder:Decoder,EncodedVideoChunk:class{constructor(v){Object.assign(this,v)}},createImageBitmap:()=>new Promise(resolve=>state.bitmaps.push(()=>resolve({width:640,height:480,close:()=>state.closed++})))};
 vm.runInContext(source,vm.createContext(sandbox));return {state,send:m=>Promise.resolve(handler({data:m}))};
}
function packet(kind,seq,payload){const b=Buffer.alloc(9+payload.length);b[0]=kind;b.writeUInt32BE(seq,1);b.writeUInt32BE(1000,5);Buffer.from(payload).copy(b,9);return {type:'frame',data:b.toString('base64')}}
{
 const h=runtime(),c={type:'video',codec:'avc1.42c01f'};
 await h.send(c);await h.send(packet(2,1,[0,0,0,1,0x67,0x42,0xc0,0x1f,0,0,0,1,0x65]));await h.send(c);await h.send(packet(3,2,[0,0,0,1,0x41]));
 assert.deepEqual(h.state.decoded,['key','delta']);console.log('PASS identical H264 metadata after keyframe preserves delta');
}
{
 const h=runtime();await h.send({type:'video',codec:'jpeg'});
 const pending=h.send(packet(1,1,[0xff,0xd8,0xff,0xd9]));assert.equal(h.state.bitmaps.length,1);
 await h.send({type:'clear'});const draws=h.state.draws;h.state.bitmaps.shift()();await pending;
 assert.equal(h.state.draws,draws,'stale JPEG painted after clear');assert.equal(h.state.closed,1);console.log('PASS asynchronous JPEG cannot repaint after clear');
}
