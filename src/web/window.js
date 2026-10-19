'use strict';
// Pure window bookkeeping, shared by all three renderers. Cursor identities
// come from the server; variable byte budgets must never be treated as pages.
const RecordWindow = {
 key(row, mode, auxiliary=false) { return String(mode==='visual'&&!auxiliary ? row.number||`b${row.offset}` : row.line||`b${row.offset}`); },
 merge(current, incoming, direction, mode, auxiliary=false) {
  const key=r=>this.key(r,mode,auxiliary), seen=new Set();
  let rows=(direction<0?[...incoming,...current]:[...current,...incoming]).filter(r=>{const k=key(r);if(seen.has(k))return false;seen.add(k);return true});
  let weight=rows.reduce((n,r)=>n+JSON.stringify(r).length,0);
  while(rows.length>240||weight>4*1024*1024&&rows.length>1){const r=direction<0?rows.pop():rows.shift();weight-=JSON.stringify(r).length}
  return rows;
 }
};
if(typeof module!=='undefined')module.exports=RecordWindow;
