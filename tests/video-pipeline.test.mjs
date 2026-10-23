import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {SequenceGate} from '../src/Windows/viewer-core.mjs';

const result=spawnSync('dotnet',['run','--project','tests/Video.Tests','-c','Release','-p:BuildInParallel=false','--','--fixture'],{encoding:'utf8',windowsHide:true});
assert.equal(result.status,0,result.stderr||result.stdout);
const fixtures=JSON.parse(result.stdout.trim().split(/\r?\n/).at(-1));
for(const fixture of fixtures){
  const sequence=new SequenceGate();let accepted=0;
  for(const frame of fixture.frames)if(sequence.accept(frame.sequence,frame.kind===2))accepted++;
  assert.equal(accepted,fixture.inputFrames,`${fixture.acknowledgmentMilliseconds}ms bridge must preserve H264 decode references at60fps`);
  console.log(`PASS actual C# queue and JavaScript sequence gate preserve600 frames with ${fixture.acknowledgmentMilliseconds}ms bridge acknowledgments`);
}
