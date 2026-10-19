export function proCardEntry(account) {
 const card=account?.activation_card
 if(!account?.email||!card)return null
 if(card.nexovcc_card_id)return {provider:'nexovcc',email:account.email,cardID:card.nexovcc_card_id}
 if(card.ejh_card_id)return {provider:'ejh',email:account.email,cardID:card.ejh_card_id}
 return null
}
export const issuerLabel=entry=>entry?.provider==='nexovcc'?'环球':'EJH'
