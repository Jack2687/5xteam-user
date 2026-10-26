const priority={unopened:0,opening:0,opened:1,waiting:2,returned:3,no_return:4}

// Rank the whole filtered set before pagination. Stable ties preserve each
// issuer's existing order, and a mixed card follows its earliest active stage.
export function sortCardsByReturnState(rows,cases=[]){
 const byCard=new Map()
 for(const row of cases){
  for(const id of new Set([row.card_id,row.ejh_card_id].filter(Boolean))){
   if(!byCard.has(id))byCard.set(id,[])
   byCard.get(id).push(row)
  }
 }
 const rank=row=>{
  const card=row.card||row,local=row.local||card.local
  if(Number(local?.pending_accounts)>0)return 0
  const associated=byCard.get(card.id)||[]
  if(!associated.length)return Number(local?.opened_accounts)>0?1:0
  return Math.min(...associated.map(c=>priority[c.state]??4))
 }
 return rows.map((row,index)=>({row,index,rank:rank(row)})).sort((a,b)=>a.rank-b.rank||a.index-b.index).map(x=>x.row)
}
