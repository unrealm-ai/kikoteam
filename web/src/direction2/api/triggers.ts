import {platformRequest,post} from './platform';
import type {Page} from './manager';
export type EventSource = 'github' | 'gitlab' | 'gitlab_token' | 'custom';
export interface TriggerConfig {source?:EventSource;prompt?:string;action?:'create_task'|'send_message';repositoryId?:number;installationId?:number;cron?:string;timezone?:string;description?:string;businessKey?:{mode:'empty'|'fixed'|'period';value?:string;period?:string};adapter?:string}
export interface TriggerGroupCandidate {id:string;provider:string;name:string;evidence:'member'|'observed'|'provider_verified'}
export interface TriggerTarget {conversation_id:string;provider:string;label:string}
export interface TriggerNotification {source_input_id:string|null;source_turn_id:string|null;task_id:string|null;reminder_event_id?:string|null;conversation_id:string;status:string;context_status:string;context_error:string|null;error_code:string|null;created_at:string}
export interface TriggerWrite {name:string;notification_targets:{conversation_id:string}[];kind:'schedule'|'webhook';enabled:boolean;config:TriggerConfig;allowed_actions:string[]}
export interface Trigger extends TriggerWrite {endpoint?:string;last_verified_at?:string;id:string;workspace_id:string;agent_id:string;version:string;created_at:string;updated_at:string;next_due:string|null}
export interface TriggerCredential {key_id:string;status:string;created_at:string;secret?:string}
export interface TriggerEvent {id:string;event_id:string;event_name?:string;execution_status?:string;action:string;binding_version:string;status:string;received_at:string;receipt:{event_id:string;status:string;task_id?:string;session_id?:string;task_space_id?:string;input_id?:string;duplicate:boolean}|null}
const base=(w:string,a:string)=>`/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/triggers`;
const path=(w:string,a:string,id:string)=>`${base(w,a)}/${encodeURIComponent(id)}`;
export const triggerLabel=(t:Trigger)=>`${t.name?t.name+' · ':''}${t.kind==='schedule'?'定时':'Webhook'} · ${t.config.description?.slice(0,45)||t.id}${t.enabled?'':'（已停用）'}`;
export const triggersApi={
 candidates:(w:string,a:string,cursor:string,signal:AbortSignal)=>platformRequest<{items:TriggerGroupCandidate[];next_cursor:string|null;truncated:boolean}>(`/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/trigger-group-candidates?${new URLSearchParams({limit:'100',...(cursor?{cursor}:{})})}`,{signal}),
 resolveCandidate:(w:string,a:string,provider:string,id:string,signal:AbortSignal)=>platformRequest<{conversation_id:string}>(`/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/trigger-group-candidates/resolve`,{...post({provider,candidate_id:id}),signal}),
 targets:(w:string,a:string,signal:AbortSignal)=>platformRequest<{items:TriggerTarget[];truncated:boolean}>(`/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/trigger-notification-targets?limit=100`,{signal}),
 notifications:(w:string,a:string,id:string,signal:AbortSignal)=>platformRequest<{items:TriggerNotification[];truncated:boolean}>(`${path(w,a,id)}/notifications?limit=100`,{signal}),
 list:(w:string,a:string,signal?:AbortSignal)=>platformRequest<{items:Trigger[]}>(base(w,a),signal?{signal}:{}),
 get:(w:string,a:string,id:string,signal:AbortSignal)=>platformRequest<Trigger>(path(w,a,id),{signal}),
 save:(w:string,a:string,body:TriggerWrite & {signing_token?:string},signal:AbortSignal,existing?:Trigger)=>platformRequest<Trigger & {initial_credential?:TriggerCredential}>(existing?path(w,a,existing.id):base(w,a),{...(existing?{method:'PUT',body:JSON.stringify({...body,version:existing.version})}:post(body)),signal}),
 credentials:(w:string,a:string,id:string,signal:AbortSignal)=>platformRequest<{items:TriggerCredential[]}>(`${path(w,a,id)}/credentials`,{signal}),
 createCredential:(w:string,a:string,id:string,signal:AbortSignal)=>platformRequest<TriggerCredential>(`${path(w,a,id)}/credentials`,{...post({}),signal}),
 importCredential:(w:string,a:string,id:string,signing_token:string,signal:AbortSignal)=>platformRequest<TriggerCredential>(`${path(w,a,id)}/credentials`,{...post({signing_token}),signal}),
 revokeCredential:(w:string,a:string,id:string,key:string,signal:AbortSignal)=>platformRequest<void>(`${path(w,a,id)}/credentials/${encodeURIComponent(key)}`,{method:'DELETE',signal}),
 events:(w:string,a:string,id:string,cursor:string,signal:AbortSignal)=>platformRequest<Page<TriggerEvent>>(`${path(w,a,id)}/events?${new URLSearchParams({limit:'20',...(cursor?{cursor}:{})})}`,{signal})
};
