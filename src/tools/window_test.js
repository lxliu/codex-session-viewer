'use strict';
// Run with Node, or with a VM containing RecordWindow and an assert function.
(function(){
 const W=typeof RecordWindow!=='undefined'?RecordWindow:require('../web/window.js');
 const check=(ok,msg)=>{if(!ok)throw Error(msg)};
 const row=n=>({line:n*2,number:n,text:'step '+n});
 const range=(a,b)=>Array.from({length:b-a+1},(_,i)=>row(a+i));
 for(const mode of ['visual','raw','json']){
  let rows=range(901,960);
  for(let i=0;i<14;i++){
   const first=rows[0].number,older=range(Math.max(1,first-60),first-1);
   rows=W.merge(rows,older,-1,mode);
   check(rows.length<=240,'DOM window cap');
   check(rows.every((r,j)=>!j||r.number===rows[j-1].number+1),'backward gap');
   check(new Set(rows.map(r=>W.key(r,mode))).size===rows.length,'duplicate keys');
  }
  for(let i=0;i<20;i++){
   const last=rows.at(-1).number;rows=W.merge(rows,range(last+1,last+60),1,mode);
   check(rows.length<=240,'forward cap');
   check(rows.every((r,j)=>!j||r.number===rows[j-1].number+1),'forward gap');
  }
 }
 const overlap=W.merge(range(10,30),range(1,12),-1,'visual');
 check(overlap.length===30&&overlap[0].number===1&&overlap.at(-1).number===30,'overlap dedup');
 const huge=range(1,120).map(r=>({...r,text:'a'.repeat(100000)}));
 check(W.merge([],huge,1,'visual').length<45,'byte window cap');
 check(W.key({line:246,number:9},'visual')==='9','visual sequence identity');
 check(W.key({line:246,number:9},'raw')==='246','physical identity');
 check(W.key({line:246,number:9},'visual',true)==='246','auxiliary physical identity');
 if(typeof console!=='undefined')console.log('record window checks passed');
})();
