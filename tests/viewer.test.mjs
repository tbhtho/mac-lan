import {parseFrame, parseMediaPacket, MediaClock, SequenceGate, mapPointer, videoFrameDuration} from '../src/Windows/viewer-core.mjs';
import assert from 'node:assert/strict';
assert.throws(()=>parseFrame(new Uint8Array(3)));
assert.throws(()=>parseFrame(new Uint8Array([9,0,0,0,0,0,0,0,0])));
const packet=new Uint8Array([2,0,0,0,1,0,0,3,232,0,0,0,1,0x65]);
const p=parseFrame(packet);assert.equal(p.sequence,1);assert.equal(p.encodeMs,1);assert.equal(p.key,true);assert.equal(p.data.length,5);
const gate=new SequenceGate();assert.equal(gate.accept(1,false),false);assert.equal(gate.accept(2,true),true);assert.equal(gate.accept(3,false),true);assert.equal(gate.accept(5,false),false);assert.equal(gate.accept(6,false),false);assert.equal(gate.accept(7,true),true);
// Preserve source geometry, including Retina pixels, inside the actual tab content bounds.
assert.deepEqual(mapPointer(200,150,400,300,1920,1080),{x:.5,y:.5});
assert.equal(mapPointer(100,0,400,300,1920,1080),null,'horizontal letterbox bars never route input');
assert.deepEqual(mapPointer(100,37.5,400,300,1920,1080),{x:.25,y:0});
// A 16:10 Mac fits 1728 x 1080 inside a 16:9 Windows viewport, with 96 px side bars.
assert.equal(mapPointer(95,540,1920,1080,2560,1600),null);
assert.deepEqual(mapPointer(96,0,1920,1080,2560,1600),{x:0,y:0},'the full Mac menu stays visible');
assert.deepEqual(mapPointer(1823,1079,1920,1080,2560,1600),{x:1727/1728,y:1079/1080},'the full Dock stays visible');
assert.equal(mapPointer(1824,540,1920,1080,2560,1600),null);
// Reserving 96 px for Windows chrome leaves 1920 x 984, with a 1574.4 px picture.
assert.equal(mapPointer(172,492,1920,984,2560,1600),null);
const menu=mapPointer(172.8,0,1920,984,2560,1600);
assert.ok(menu && Math.abs(menu.x)<1e-12 && menu.y===0,'tab-reserved height does not crop the menu');
assert.deepEqual(mapPointer(960,492,1920,984,2560,1600),{x:.5,y:.5},'Retina source pixels do not offset logical pointer coordinates');
assert.deepEqual(mapPointer(960,492,1920,984,1280,800),{x:.5,y:.5},'logical point and Retina pixel aspect ratios map identically');
assert.equal(mapPointer(0,0,1920,984,2560,1600,'fill'),null,'legacy fill metadata cannot stretch pointer mapping');
for(const point of [[-1,0],[0,-1],[400,0],[0,300]])assert.equal(mapPointer(...point,400,300,1920,1080),null);
for(const dimensions of [[0,300,1920,1080],[400,0,1920,1080],[400,300,0,1080],[400,300,1920,0],[NaN,300,1920,1080]])assert.equal(mapPointer(10,10,...dimensions),null);
assert.equal(mapPointer(100,0,400,300,1920,1080,'fit'),null,'individual-window letterbox bars never route input');
assert.deepEqual(mapPointer(200,150,400,300,1920,1080,'fit'),{x:.5,y:.5});
assert.equal(videoFrameDuration({fps:60}),1000000/60);
for(const fps of [null,undefined,0,-1,61,'60',60.5])assert.equal(videoFrameDuration({fps}),1000000/60);
console.log('PASS frame envelope, loss recovery at keyframe, pointer mapping');

for(const kind of [1,2,3,4]){
 const bytes=new Uint8Array(21),v=new DataView(bytes.buffer);bytes[0]=kind|0x80;v.setUint32(1,23);v.setUint32(9,1);v.setUint32(13,500);
 const value=parseMediaPacket(bytes);assert.equal(value.kind,kind);assert.equal(value.captureMs,(4294967296+500)/1000);assert.equal(value.data.length,4);
 if(kind===4)assert.throws(()=>parseFrame(bytes));else assert.equal(parseFrame(bytes).sequence,23);
 assert.throws(()=>parseMediaPacket(bytes.subarray(0,17)));
 v.setUint32(9,0xffffffff);assert.throws(()=>parseMediaPacket(bytes));
}
{
 const clock=new MediaClock();clock.observe(1000,10000);assert.equal(clock.due(1000),10060);
 assert.equal(clock.observe(10000,10001),true);assert.equal(clock.due(10000),10061,'fresh media corrects a stale initial image in one step');
 clock.observe(1000,10002);assert.equal(clock.due(10000),10061,'old interleaved samples cannot rebase the clock upward');
 clock.reset();assert.equal(clock.due(1000),null);
 clock.observe(1000,100);clock.observe(1010,120);assert.ok(clock.due(1010)>=170&&clock.due(1010)<=170.04,'ordinary arrival jitter cannot move playout by more than slow correction');
 clock.ensureLead(500);assert.equal(clock.lead,200,'device lead is capped');
}
console.log('PASS extended media envelopes and shared clock discontinuity/drift bounds');
