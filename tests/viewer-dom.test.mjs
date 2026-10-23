// Real Chromium DOM events in an isolated headless browser. No visible windows,
// desktop capture, native keyboard hooks, or OS input injection; loopback content only.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import http from 'node:http';
import {spawn} from 'node:child_process';

const candidates=[process.env.MACDOW_HEADLESS_BROWSER,
  'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
  'C:/Program Files/Microsoft/Edge/Application/msedge.exe',
  'C:/Program Files/Google/Chrome/Application/chrome.exe'].filter(Boolean);
let executable;for(const candidate of candidates){try{await fs.access(candidate);executable=candidate;break;}catch{}}
if(!executable)throw new Error('A Chromium browser is required for the headless DOM checks. Set MACDOW_HEADLESS_BROWSER to its executable.');
await fs.mkdir('.cache',{recursive:true});
// Keep the isolated browser cache with other ignored test artifacts. Windows may
// briefly retain cache-file handles after the browser exits.
const root=path.resolve('src/Windows'),profile=await fs.mkdtemp(path.resolve('.cache/viewer-dom-'));
const server=http.createServer(async(req,res)=>{
  const file=req.url==='/'?'viewer.html':req.url?.slice(1);
  if(!['viewer.html','viewer.mjs','viewer-core.mjs'].includes(file)){res.writeHead(404);res.end();return;}
  res.setHeader('Content-Type',file.endsWith('.mjs')?'text/javascript':'text/html');
  res.end(await fs.readFile(path.join(root,file)));
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const browser=spawn(executable,['--headless=new','--disable-gpu','--disable-extensions','--disable-background-mode','--disable-background-networking','--disable-component-update','--disable-sync','--no-first-run','--no-default-browser-check','--remote-debugging-port=0','--user-data-dir='+profile,'about:blank'],{windowsHide:true,stdio:'ignore'});
let socket,closing=false,closeBrowser;
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function until(read,description){for(let i=0;i<150;i++){const value=await read();if(value)return value;await delay(20);}throw new Error('Timed out: '+description);}
try{
  const port=await until(async()=>{try{return(await fs.readFile(path.join(profile,'DevToolsActivePort'),'utf8')).split('\n')[0];}catch{return null;}},'headless browser startup');
  const pages=await(await fetch('http://127.0.0.1:'+port+'/json/list')).json();
  socket=new WebSocket(pages.find(p=>p.type==='page').webSocketDebuggerUrl);
  await new Promise((resolve,reject)=>{socket.onopen=resolve;socket.onerror=reject;});
  let id=0;const pending=new Map();
  socket.onmessage=e=>{const m=JSON.parse(e.data);if(!m.id)return;const call=pending.get(m.id);pending.delete(m.id);if(call)m.error?call.reject(new Error(m.error.message)):call.resolve(m.result);};
  socket.onclose=()=>{for(const call of pending.values())call.reject(new Error(closing?'Browser closed':'Headless browser disconnected'));pending.clear();};
  const send=(method,params={})=>new Promise((resolve,reject)=>{const number=++id;pending.set(number,{resolve,reject});socket.send(JSON.stringify({id:number,method,params}));});
  closeBrowser=()=>send('Browser.close').catch(()=>{});
  const evaluate=async expression=>{const result=await send('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if(result.exceptionDetails)throw new Error(result.exceptionDetails.text+': '+result.exceptionDetails.exception?.description);return result.result.value;};
  await send('Page.enable');
  await send('Emulation.setDeviceMetricsOverride',{width:1920,height:984,deviceScaleFactor:1,mobile:false});
  await send('Page.addScriptToEvaluateOnNewDocument',{source:`
    window.messages=[];window.activationCapture=[];window.focusCalls=0;let callback;
    window.receive=m=>callback({data:m});
    chrome.webview={addEventListener:(_,handler)=>callback=handler,postMessage:message=>{
      messages.push(message);
      if(message.type==='requestControl'){
        const canvas=document.querySelector('canvas');
        activationCapture.push(canvas.hasPointerCapture(1));
        // The native activation boundary may replace focus/capture. The viewer
        // must finish its activation press before asking the native owner.
        if(canvas.hasPointerCapture(1))canvas.releasePointerCapture(1);
        setTimeout(()=>receive({type:'armed',active:true,epoch:message.epoch}),0);
      }
    }};
    const focus=HTMLElement.prototype.focus;HTMLElement.prototype.focus=function(...args){focusCalls++;return focus.apply(this,args);};
  `});
  await send('Page.navigate',{url:'http://127.0.0.1:'+server.address().port+'/'});
  await until(()=>evaluate('window.messages?.some(m=>m.type==="loaded")'),'viewer module load');
  assert.equal(await evaluate('getComputedStyle(document.querySelector("canvas")).objectFit'),'contain','the first picture preserves aspect ratio before any metadata arrives');
  await evaluate('document.querySelector("canvas").width=2560;document.querySelector("canvas").height=1600;receive({type:"clear",epoch:1});receive({type:"video",codec:"jpeg",epoch:1,presentation:"fill"})');
  assert.equal(await evaluate('getComputedStyle(document.querySelector("canvas")).objectFit'),'contain','legacy fill metadata must not stretch a Retina Mac desktop');
  assert.deepEqual(await evaluate('messages.filter(m=>m.type==="clearAck").map(m=>m.epoch)'),[1],'a real DOM stream starts by acknowledging its clear epoch');
  await evaluate('messages.length=0');
  for(const type of ['mousePressed','mouseReleased'])await send('Input.dispatchMouseEvent',{type,x:50,y:492,button:'left',clickCount:1});
  assert.deepEqual(await evaluate('messages'),[],'clicking a side bar cannot arm control');
  const press=()=>send('Input.dispatchMouseEvent',{type:'mousePressed',x:320,y:240,button:'left',clickCount:1});
  const release=()=>send('Input.dispatchMouseEvent',{type:'mouseReleased',x:320,y:240,button:'left',clickCount:1});
  await evaluate('messages.length=0');await press();
  assert.deepEqual(await evaluate('messages.map(m=>m.type)'),[],'an unfinished activation press must not cross into native focus handling');
  await release();await until(()=>evaluate('activationCapture.length===1'),'activation click commit');
  assert.deepEqual(await evaluate('activationCapture'),[false],'native activation starts only after DOM pointer capture is released');
  await until(()=>evaluate('document.body.classList.contains("armed")'),'native activation acknowledgement');
  await evaluate('for(let i=0;i<8;i++)receive({type:"armed",active:true,epoch:1})');
  assert.equal(await evaluate('focusCalls'),1,'periodic armed status must not refocus the already focused canvas');
  await press();await release();
  assert.deepEqual(await evaluate('messages.map(m=>m.type)'),['requestControl','move','button','button'],'first complete click is consumed and the next click reaches the Mac');
  assert.deepEqual(await evaluate('messages.filter(m=>m.type==="button").map(m=>m.down)'),[true,false]);
  assert.equal(await evaluate('messages.every(m=>m.epoch===1)'),true,'activation and routed pointer input carry the current stream epoch');
  await evaluate('messages.length=0');
  await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:960,y:492});
  const center=await evaluate('messages.find(m=>m.type==="move")');
  assert.equal(center.x,.5);assert.equal(center.y,.5,'the Retina desktop center matches the tab content center');
  await evaluate('messages.length=0');
  for(const type of ['mousePressed','mouseReleased'])await send('Input.dispatchMouseEvent',{type,x:50,y:492,button:'left',clickCount:1});
  await send('Input.dispatchMouseEvent',{type:'mouseWheel',x:50,y:492,deltaX:0,deltaY:80});
  assert.deepEqual(await evaluate('messages'),[],'armed side bars cannot inject clicks or scrolling');
  await evaluate('window.dispatchEvent(new Event("blur"))');
  assert.equal(await evaluate('messages.at(-1).type'),'blur','true focus loss still revokes control');
  assert.equal(await evaluate('messages.at(-1).reason'),'viewer-blur','focus loss carries a fixed diagnostic release reason');
  await evaluate('receive({type:"armed",active:false,epoch:1});messages.length=0');
  await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:340,y:240});
  assert.deepEqual(await evaluate('messages'),[],'focus loss suppresses mouse input until another explicit activation click');
  await evaluate('receive({type:"clear",epoch:2});receive({type:"armed",active:true,epoch:1});messages.length=0');
  await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:350,y:240});
  assert.deepEqual(await evaluate('messages'),[],'a stale armed acknowledgement cannot reactivate the new stream');
  console.log('PASS real headless DOM epoch handshake, safe click activation, routed pointer input, stale armed rejection and blur release');
}finally{
  closing=true;if(closeBrowser)void closeBrowser();
  for(let i=0;i<100&&browser.exitCode===null;i++)await delay(20);
  socket?.close();if(browser.exitCode===null)browser.kill();server.close();
}
