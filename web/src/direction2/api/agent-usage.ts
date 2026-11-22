import { platformRequest } from './platform';
export type UsageChannel = 'web'|'feishu'|'dingtalk'|'wecom';
export interface UsageSelector { channel: UsageChannel; kind:'user'|'department'|'group'; id:string; binding_id?:string; identity_epoch?:number; include_descendants?:boolean }
export interface UsagePattern { text:string; channels:UsageChannel[] }
export interface UsageConfig { access:{mode:'all'|'allowlist';group_members_enabled:boolean;allow:UsageSelector[];deny:UsageSelector[]};trigger:{mention:boolean;keywords:UsagePattern[];regexes:UsagePattern[]};history:{enabled:boolean;limit:number;channels:UsageChannel[]} }
export interface UsageResource { version:number;usage_config:UsageConfig }
const path=(w:string,a:string)=>`/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/usage-config`;
export const usageApi={get:(w:string,a:string,signal:AbortSignal)=>platformRequest<UsageResource>(path(w,a),{signal}),save:(w:string,a:string,body:UsageResource,signal:AbortSignal)=>platformRequest<UsageResource>(path(w,a),{method:'PATCH',body:JSON.stringify(body),signal})};
