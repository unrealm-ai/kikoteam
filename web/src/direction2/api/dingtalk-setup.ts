import { platformRequest } from './platform';
export interface DingTalkRegistration { id:string; state:string; app_id:string; verification_url?:string; expires_at:string; next_poll_at:string; credential_saved:boolean; error_code:string; retry_until?:string; retry_status?:string }
export interface DingTalkSetupStatus { registration:DingTalkRegistration|null }
export function dingTalkVerificationURL(value?:string):string|null{try{const u=new URL(value||'');return u.protocol==='https:'&&!u.username&&!u.password&&!u.port&&!u.hash&&(u.hostname==='dingtalk.com'||u.hostname.endsWith('.dingtalk.com'))?u.href:null;}catch{return null;}}
const path=(w:string,a:string)=>`/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/channels/dingtalk/registration`;
const bounded=(signal:AbortSignal)=>AbortSignal.any([signal,AbortSignal.timeout(25000)]);
export const dingTalkSetupApi={
 get:(w:string,a:string,signal:AbortSignal)=>platformRequest<DingTalkSetupStatus>(path(w,a),{signal:bounded(signal)}),
 create:(w:string,a:string,key:string,signal:AbortSignal)=>platformRequest<DingTalkSetupStatus>(path(w,a),{method:'POST',headers:{'Idempotency-Key':key},body:'{}',signal:bounded(signal)}),
 action:(w:string,a:string,id:string,action:'poll'|'confirm'|'cancel',signal:AbortSignal)=>platformRequest<DingTalkSetupStatus>(`${path(w,a)}/${encodeURIComponent(id)}/${action}`,{method:'POST',body:'{}',signal:bounded(signal)})
};
