import {concurrencyDetails,forecastDetails} from './restockForecast.js'
import {restockMonitorStatus} from './restockMonitor.js'

// Historical values remain inspectable, with their original timestamp. These
// projections are display-only; monitoring and admission keep the real clock.
export function restockDisplay(state={},now=Date.now()) {
 const snapshot=state.snapshot||{},decision=state.decision||{},config=state.config||{}
 const at=Date.parse(snapshot.at||''),decisionAt=Date.parse(decision.snapshot?.at||'')
 const sameGroup=snapshot.group_id===config.group_id
 const sampleKnown=sameGroup&&snapshot.valid&&snapshot.load_valid&&Number.isFinite(at)&&at<=now+5000
 const decisionKnown=decision.version===config.version&&decision.snapshot?.group_id===config.group_id&&decision.snapshot?.binding===snapshot.binding&&decision.snapshot?.valid&&decision.snapshot?.load_valid&&Number.isFinite(decisionAt)&&decisionAt<=now+5000
 const status=restockMonitorStatus(state,now)
 const oldDecision=now-decisionAt>(config.fresh_seconds||20)*1000
 const historicalSample=sampleKnown&&status.unavailable
 const historicalDecision=decisionKnown&&(status.unavailable||oldDecision)
 const viewDecision={...decision,config,...(!decisionKnown?{snapshot:{...decision.snapshot,valid:false}}:{})}
 const forecastNow=historicalDecision?decisionAt:now
 return {
  concurrency:concurrencyDetails(snapshot,!sampleKnown,historicalSample?at:now,config),
  forecast:forecastDetails(viewDecision,forecastNow),forecastDecision:viewDecision,forecastNow,
  decisionUnavailable:!decisionKnown,
  historical:historicalSample||historicalDecision,
  sampleAt:snapshot.at,decisionAt:decision.snapshot?.at,
  usable:sampleKnown?(snapshot.accounts||[]).filter(a=>a.ready&&(!a.expires_at||Date.parse(a.expires_at)>(historicalSample?at:now))).length:null,
 }
}
