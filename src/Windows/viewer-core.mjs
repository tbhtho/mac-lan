export function parseFrame(bytes) {
  if(bytes.length<10||![1,2,3].includes(bytes[0]))throw Error('Invalid frame envelope');
  const v=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
  return {kind:bytes[0],key:bytes[0]===2,sequence:v.getUint32(1),encodeMs:v.getUint32(5)/1000,data:bytes.subarray(9)};
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
