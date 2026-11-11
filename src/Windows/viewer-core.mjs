export function parseMediaPacket(bytes) {
  if(bytes.length<10)throw Error('Invalid media envelope');
  const kind=bytes[0]&0x7f,header=(bytes[0]&0x80)?17:9;
  if(kind<1||kind>4||bytes.length<=header)throw Error('Invalid media envelope');
  const v=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
  const micros=header===17?v.getUint32(9)*4294967296+v.getUint32(13):0;
  if(!Number.isSafeInteger(micros))throw Error('Invalid capture timestamp');
  return {kind,key:kind===2,sequence:v.getUint32(1),encodeMs:v.getUint32(5)/1000,captureMs:micros>0?micros/1000:null,data:bytes.subarray(header)};
}
export function parseFrame(bytes) {
  const packet=parseMediaPacket(bytes);if(packet.kind===4)throw Error('Audio is not video');return packet;
}
// Both tracks use the same remote capture clock, mapped onto performance.now().
// The minimum arrival offset rejects queueing jitter. Slow correction (2 ms/s)
// follows independent clock drift without moving scheduled sound abruptly.
// This is a playout clock, not a measurement of network or physical AV latency.
export class MediaClock {
  constructor(){this.reset();}
  reset(){this.offset=null;this.samples=[];this.lastNow=null;this.latestCapture=null;this.lead=60;}
  ensureLead(ms){const lead=Math.max(60,Math.min(200,ms));if(lead>this.lead){if(this.offset!==null)this.offset+=lead-this.lead;this.lead=lead;}}
  observe(captureMs,now){
    if(!Number.isFinite(captureMs)||captureMs<=0||!Number.isFinite(now)||this.latestCapture!==null&&captureMs<=this.latestCapture)return false;
    this.latestCapture=captureMs;
    this.samples=this.samples.filter(s=>now-s.now<=2000);
    this.samples.push({now,offset:now-captureMs});
    if(this.samples.length>512)this.samples.shift();
    const target=Math.min(...this.samples.map(s=>s.offset))+this.lead;
    const discontinuity=this.offset!==null&&target<this.offset-250;
    if(this.offset===null||discontinuity)this.offset=target;
    else {const step=Math.max(0,Math.min(100,now-this.lastNow))*0.002;this.offset+=Math.max(-step,Math.min(step,target-this.offset));}
    this.lastNow=now;return discontinuity;
  }
  due(captureMs){return this.offset!==null&&Number.isFinite(captureMs)&&captureMs>0?captureMs+this.offset:null;}
}
export function videoFrameDuration(metadata){const fps=metadata?.fps;return 1000000/(Number.isInteger(fps)&&fps>=5&&fps<=60?fps:60);}
export class SequenceGate {
  constructor(){this.last=null;this.waiting=true;}
  accept(sequence,key){if(this.last!==null&&sequence!==((this.last+1)>>>0))this.waiting=true;this.last=sequence;if(key)this.waiting=false;return !this.waiting;}
  reset(){this.last=null;this.waiting=true;}
}
// Match the complete aspect-fit picture; bars never address the remote desktop.
export function mapPointer(x,y,w,h,iw,ih){
  if(![x,y,w,h,iw,ih].every(Number.isFinite)||w<=0||h<=0||iw<=0||ih<=0)return null;
  const scale=Math.min(w/iw,h/ih),cw=iw*scale,ch=ih*scale;x-=(w-cw)/2;y-=(h-ch)/2;w=cw;h=ch;
  return x<0||y<0||x>=w||y>=h?null:{x:x/w,y:y/h};
}
