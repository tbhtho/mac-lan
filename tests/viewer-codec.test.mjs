// Optional real-codec check: synthetic FFmpeg pictures only, in isolated
// headless Chromium. No desktop capture, audio, hooks, or OS input injection.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import http from 'node:http';
import {spawn,spawnSync} from 'node:child_process';

const encoded=spawnSync(process.env.MACDOW_FFMPEG||'ffmpeg',[
  '-hide_banner','-loglevel','error','-f','lavfi','-i','testsrc2=size=96x64:rate=60',
  '-frames:v','20','-an','-c:v','libx264','-profile:v','baseline','-pix_fmt','yuv420p',
  '-bf','0','-threads','1','-preset','ultrafast','-tune','zerolatency',
  '-x264-params','aud=1:repeat-headers=1:keyint=30:min-keyint=30:scenecut=0',
  '-f','h264','pipe:1'],{windowsHide:true,maxBuffer:2*1024*1024,timeout:10000});
if(encoded.error?.code==='ENOENT'){
  console.log('SKIP optional real H.264 check: FFmpeg is unavailable. Set MACDOW_FFMPEG to an existing executable.');
  process.exit(0);
}
assert.equal(encoded.status,0,encoded.error?.message||encoded.stderr.toString());
// AUD delimiters split access units. Remove just the delimiter NAL so an IDR
// packet begins with SPS, as the production helper's Annex B packets do.
const nals=[];
for(let i=0;i<encoded.stdout.length-3;i++){
  let prefix=0;
  if(encoded.stdout[i]===0&&encoded.stdout[i+1]===0){
    if(encoded.stdout[i+2]===1)prefix=3;
    else if(encoded.stdout[i+2]===0&&encoded.stdout[i+3]===1)prefix=4;
  }
  if(prefix){nals.push({start:i,body:i+prefix,type:encoded.stdout[i+prefix]&31});i+=prefix-1;}
}
const accessUnits=[];let unit=[];
for(let i=0;i<nals.length;i++){
  const nal=nals[i];
  if(nal.type===9){if(unit.length)accessUnits.push(unit);unit=[];continue;}
  unit.push({type:nal.type,data:encoded.stdout.subarray(nal.start,nals[i+1]?.start??encoded.stdout.length)});
}
if(unit.length)accessUnits.push(unit);
assert.equal(accessUnits.length,20,'the fixture must contain exactly twenty synthetic access units');
assert.equal(accessUnits.filter(frame=>frame.some(nal=>nal.type===5)).length,1,'the fixture must include dependent delta pictures');
const sps=nals.find(nal=>nal.type===7);assert.ok(sps,'the fixture must contain SPS');
const codec='avc1.'+encoded.stdout.subarray(sps.body+1,sps.body+4).toString('hex');
const packets=accessUnits.map((frame,index)=>{
  const data=Buffer.concat(frame.map(nal=>nal.data));
  const envelope=Buffer.alloc(9+data.length);
  envelope[0]=frame.some(nal=>nal.type===5)?2:3;envelope.writeUInt32BE(index+1,1);envelope.writeUInt32BE(1000,5);data.copy(envelope,9);
  return envelope.toString('base64');
});

const candidates=[process.env.MACDOW_HEADLESS_BROWSER,
  'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
  'C:/Program Files/Microsoft/Edge/Application/msedge.exe',
  'C:/Program Files/Google/Chrome/Application/chrome.exe'].filter(Boolean);
let executable;for(const candidate of candidates){try{await fs.access(candidate);executable=candidate;break;}catch{}}
if(!executable){console.log('SKIP optional real H.264 check: no headless Chromium executable. Set MACDOW_HEADLESS_BROWSER.');process.exit(0);}
await fs.mkdir('.cache',{recursive:true});
const root=path.resolve('src/Windows'),profile=await fs.mkdtemp(path.resolve('.cache/viewer-codec-'));
const server=http.createServer(async(req,res)=>{
  const file=req.url==='/'?'viewer.html':req.url?.slice(1);
  if(!['viewer.html','viewer.mjs','viewer-core.mjs'].includes(file)){res.writeHead(404);res.end();return;}
  res.setHeader('Content-Type',file.endsWith('.mjs')?'text/javascript':'text/html');
  res.end(await fs.readFile(path.join(root,file)));
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const browser=spawn(executable,['--headless=new','--disable-gpu','--disable-extensions','--disable-background-mode',
  '--disable-background-networking','--disable-component-update','--disable-sync','--no-first-run','--no-default-browser-check',
  '--remote-debugging-port=0','--user-data-dir='+profile,'about:blank'],{windowsHide:true,stdio:'ignore'});
let socket,closing=false,closeBrowser;
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const trace=message=>{if(process.env.MACDOW_CODEC_DEBUG)console.log(message);};
async function until(read,description){for(let i=0;i<200;i++){const value=await read();if(value)return value;await delay(20);}throw new Error('Timed out: '+description);}
try{
  const port=await until(async()=>{try{return(await fs.readFile(path.join(profile,'DevToolsActivePort'),'utf8')).split('\n')[0];}catch{return null;}},'headless browser startup');
  trace('Browser port ready');
  const pages=await(await fetch('http://127.0.0.1:'+port+'/json/list')).json();
  socket=new WebSocket(pages.find(p=>p.type==='page').webSocketDebuggerUrl);
  await new Promise((resolve,reject)=>{socket.onopen=resolve;socket.onerror=reject;});
  let id=0;const pending=new Map();
  socket.onmessage=e=>{const m=JSON.parse(e.data);if(!m.id)return;const call=pending.get(m.id);pending.delete(m.id);if(call)m.error?call.reject(new Error(m.error.message)):call.resolve(m.result);};
  socket.onclose=()=>{for(const call of pending.values())call.reject(new Error(closing?'Browser closed':'Headless browser disconnected'));pending.clear();};
  const send=(method,params={})=>new Promise((resolve,reject)=>{const number=++id,timer=setTimeout(()=>{pending.delete(number);reject(new Error('Timed out CDP call: '+method));},10000);pending.set(number,{resolve:value=>{clearTimeout(timer);resolve(value);},reject:error=>{clearTimeout(timer);reject(error);}});socket.send(JSON.stringify({id:number,method,params}));});
  closeBrowser=()=>send('Browser.close').catch(()=>{});
  const evaluate=async expression=>{const result=await send('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if(result.exceptionDetails)throw new Error(result.exceptionDetails.text+': '+result.exceptionDetails.exception?.description);return result.result.value;};
  await send('Page.enable');
  await send('Page.addScriptToEvaluateOnNewDocument',{source:`
    window.messages=[];window.codecProbe={decodes:[],outputs:[],paints:[],held:[],errors:[],closed:0,maxQueue:0,hold:false};let callback;
    window.receive=m=>callback({data:m});
    chrome.webview={addEventListener:(_,handler)=>callback=handler,postMessage:message=>messages.push(message)};
    window.NativeVideoDecoder=window.VideoDecoder;
    if(NativeVideoDecoder)window.VideoDecoder=class extends NativeVideoDecoder{
      constructor(init){super({...init,error:error=>{codecProbe.errors.push(error.message);init.error(error);},output:frame=>{
        codecProbe.outputs.push(frame.timestamp);
        const close=frame.close.bind(frame);let closed=false;
        frame.close=()=>{if(closed)throw Error('A real VideoFrame was closed twice');closed=true;codecProbe.closed++;close();};
        if(codecProbe.hold)codecProbe.held.push(()=>init.output(frame));else init.output(frame);
      }});}
      // This CPU-only harness disables GPU access. Override only the optional
      // acceleration preference; all decode work and callbacks remain native.
      configure(config){super.configure({...config,hardwareAcceleration:'prefer-software'});}
      decode(chunk){codecProbe.decodes.push({type:chunk.type,timestamp:chunk.timestamp});super.decode(chunk);codecProbe.maxQueue=Math.max(codecProbe.maxQueue,this.decodeQueueSize);}
    };
    const draw=CanvasRenderingContext2D.prototype.drawImage;
    CanvasRenderingContext2D.prototype.drawImage=function(...args){const result=draw.apply(this,args);codecProbe.paints.push(args[0].timestamp);return result;};
  `});
  await send('Page.navigate',{url:'http://127.0.0.1:'+server.address().port+'/'});
  await until(()=>evaluate('window.messages?.some(m=>m.type==="loaded")'),'production viewer module load');
  trace('Production viewer loaded');
  const supported=await evaluate(`NativeVideoDecoder?NativeVideoDecoder.isConfigSupported({codec:${JSON.stringify(codec)},hardwareAcceleration:'prefer-software',optimizeForLatency:true}).then(v=>v.supported):false`);
  if(!supported){console.log('SKIP optional real H.264 check: this headless Chromium build does not support the synthetic baseline codec.');}
  else{
    const receive=message=>evaluate(`receive(${JSON.stringify(message)})`);
    await receive({type:'clear',epoch:1});
    assert.deepEqual(await evaluate('messages.filter(m=>m.type==="clearAck")'),[{type:'clearAck',epoch:1}]);
    await receive({type:'video',codec,fps:60,epoch:1});
    trace('Codec metadata accepted');
    for(let offset=0;offset<packets.length;offset+=8){
      const batch=1+offset/8;
      await receive({type:'frames',epoch:1,batch,frames:packets.slice(offset,offset+8)});
      assert.equal(await evaluate(`messages.filter(m=>m.type==='frameAck'&&m.epoch===1&&m.batch===${batch}).length`),1,'each admitted production batch must acknowledge once');
    }
    trace('All synthetic batches admitted');
    try{await until(()=>evaluate('codecProbe.outputs.length===20'),'all twenty dependent pictures to decode');}
    catch(error){throw new Error(error.message+'; '+JSON.stringify(await evaluate('({decodes:codecProbe.decodes.length,outputs:codecProbe.outputs.length,errors:codecProbe.errors,hint:document.querySelector("#hint").textContent})')));}
    await until(()=>evaluate('codecProbe.paints.at(-1)===codecProbe.outputs.at(-1)'),'latest actual decoded frame to paint');
    trace('All native outputs and latest paint observed');
    const complete=await evaluate('({decodes:codecProbe.decodes,outputs:codecProbe.outputs,paints:codecProbe.paints,maxQueue:codecProbe.maxQueue,closed:codecProbe.closed,pixel:Array.from(document.querySelector("canvas").getContext("2d").getImageData(10,10,1,1).data),hint:document.querySelector("#hint").textContent})');
    assert.equal(complete.decodes.length,20,'no encoded H.264 reference may disappear from the batch/dequeue pipeline');
    assert.deepEqual(complete.decodes.map(v=>v.type),['key',...Array(19).fill('delta')]);
    assert.deepEqual(complete.outputs,complete.decodes.map(v=>v.timestamp),'real decoder output must retain the complete reference order');
    assert.ok(complete.maxQueue<=2,'the production decoder submission queue must remain bounded');
    assert.equal(complete.closed,20,'all real decoded frames must be closed after presentation or replacement');
    assert.equal(complete.pixel[3],255);assert.ok(complete.pixel.slice(0,3).some(v=>v>10),'the actual canvas must contain the synthetic picture');
    assert.ok(!/reset|delayed|error/i.test(complete.hint),complete.hint);
    console.log('PASS real software H.264: twenty ordered dependent pictures survive batches and asynchronous dequeue, paint actual pixels, and close every VideoFrame');

    await receive({type:'clear',epoch:2});await receive({type:'video',codec,fps:60,epoch:2});
    await receive({type:'armed',active:true,epoch:2});
    await evaluate('codecProbe.hold=true');
    // Two retained native pictures are enough to cross the clear boundary
    // without depending on a codec-specific decoded-picture pool size.
    await receive({type:'frames',epoch:2,batch:1,frames:packets.slice(0,2)});
    await until(()=>evaluate('codecProbe.held.length===2'),'two bounded actual decoder outputs');
    trace('Holding two actual decoded pictures');
    const before=await evaluate('({paints:codecProbe.paints.length,closed:codecProbe.closed,decodes:codecProbe.decodes.length})');
    await receive({type:'clear',epoch:3});
    await receive({type:'armed',active:true,epoch:2});
    await receive({type:'video',codec,fps:60,epoch:2});
    await receive({type:'frames',epoch:2,batch:2,frames:packets.slice(8,16)});
    await evaluate('for(const deliver of codecProbe.held.splice(0))deliver();document.querySelector("canvas").dispatchEvent(new PointerEvent("pointermove",{clientX:10,clientY:10,pointerId:1,bubbles:true}));');
    // Wait actual animation frames so stale scheduled presentation has a chance
    // to run. This is browser scheduling, not a throughput measurement.
    await evaluate('new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');
    const after=await evaluate('({paints:codecProbe.paints.length,closed:codecProbe.closed,decodes:codecProbe.decodes.length,armed:document.body.classList.contains("armed"),messages:messages.filter(m=>["move","button","scroll","requestControl"].includes(m.type)),clears:messages.filter(m=>m.type==="clearAck").map(m=>m.epoch)})');
    assert.equal(after.paints,before.paints,'real decoded output retained across clear must never repaint');
    assert.equal(after.closed,before.closed+2,'every stale real decoded frame must be closed');
    assert.equal(after.decodes,before.decodes,'stale frame batches must not enter the new decoder epoch');
    assert.equal(after.armed,false,'stale armed status must not restore control');assert.deepEqual(after.messages,[]);
    assert.deepEqual(after.clears,[1,2,3],'each clear handshake must acknowledge its exact epoch');
    console.log('PASS real decoded output held across clear closes without painting; stale video, batches and armed status cannot restore an ended stream');
    console.log('LIMIT synthetic 96x64 software decode validates codec correctness only; it does not measure desktop, hardware acceleration, network latency or achieved 60 fps');
  }
}finally{
  closing=true;if(closeBrowser)void closeBrowser();
  for(let i=0;i<100&&browser.exitCode===null;i++)await delay(20);
  socket?.close();if(browser.exitCode===null)browser.kill();server.close();
  server.closeAllConnections();
}
