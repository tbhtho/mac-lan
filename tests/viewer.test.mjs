import {parseFrame, SequenceGate, mapPointer} from '../src/Windows/viewer-core.mjs';
import assert from 'node:assert/strict';
assert.throws(()=>parseFrame(new Uint8Array(3)));
assert.throws(()=>parseFrame(new Uint8Array([9,0,0,0,0,0,0,0,0])));
const packet=new Uint8Array([2,0,0,0,1,0,0,3,232,0,0,0,1,0x65]);
const p=parseFrame(packet);assert.equal(p.sequence,1);assert.equal(p.encodeMs,1);assert.equal(p.key,true);assert.equal(p.data.length,5);
const gate=new SequenceGate();assert.equal(gate.accept(1,false),false);assert.equal(gate.accept(2,true),true);assert.equal(gate.accept(3,false),true);assert.equal(gate.accept(5,false),false);assert.equal(gate.accept(6,false),false);assert.equal(gate.accept(7,true),true);
assert.deepEqual(mapPointer(200,150,400,300,1920,1080),{x:.5,y:.5});assert.equal(mapPointer(100,0,400,300,1920,1080),null);
console.log('PASS frame envelope, loss recovery at keyframe, pointer mapping');
