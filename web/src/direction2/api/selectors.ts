import {platformRequest,post} from './platform';
import type {UsageChannel,UsageSelector} from './agent-usage';
export type SelectorPurpose='usage'|'capability'|'sharing'|'confirmation'|'notification';
export interface SelectionRef {id?:string;kind:'user'|'department'|'group';provider:UsageChannel;workspace_id:string;agent_id:string;binding_id?:string;tenant_id?:string;identity_epoch?:number;connection_generation?:number;identity_namespace?:string;config_revision?:number;group_epoch?:number;agent_epoch?:number;external_id:string}
export interface SelectionCandidate {ref:SelectionRef;display_name?:string;name?:string;provider?:UsageChannel;status:string;selectable:boolean;disabled_reason?:string;verification_status?:string;search_user_id?:string;verification_id?:string;department_paths?:string[];parent_id?:string;evidence?:string;coverage?:string;selection_token?:string}
export interface SelectionPage {items:SelectionCandidate[];next_cursor:string|null;limited?:boolean;snapshot_version?:number}
const path=(w:string,a:string)=>`/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}`;
export const selectorKey=(ref:SelectionRef)=>JSON.stringify([ref.workspace_id,ref.agent_id,ref.provider,ref.binding_id||'',ref.identity_epoch||0,ref.kind,ref.identity_namespace,ref.external_id]);
export const toUsageSelector=(ref:SelectionRef,includeDescendants=false):UsageSelector=>({channel:ref.provider,kind:ref.kind,id:ref.external_id,...(ref.provider==='web'?{}:{binding_id:ref.binding_id,identity_epoch:ref.identity_epoch}),...(ref.kind==='department'?{include_descendants:includeDescendants}:{})});
export interface PeopleSource {provider:UsageChannel;available:boolean;unavailable_reason?:string;supports_department_tree:boolean;supports_browse:boolean;supports_pagination:boolean}
export const selectorsApi={
 resolveSavedGroups:(w:string,a:string,selectors:UsageSelector[],signal:AbortSignal)=>platformRequest<{items:SelectionCandidate[]}>(`${path(w,a)}/selectors/groups/resolve`,{...post({selectors}),signal}),
 resolveSaved:(w:string,a:string,selectors:UsageSelector[],purpose:SelectorPurpose,signal:AbortSignal)=>platformRequest<{items:SelectionCandidate[]}>(`${path(w,a)}/selectors/people/resolve`,{...post({purpose,selectors}),signal}),
 sources:(w:string,a:string,purpose:SelectorPurpose,signal:AbortSignal)=>platformRequest<{items:PeopleSource[]}>(`${path(w,a)}/selectors/people/sources?${new URLSearchParams({purpose})}`,{signal}),
 search:(w:string,a:string,kind:'people'|'departments'|'groups',provider:UsageChannel,q:string,cursor:string,purpose:SelectorPurpose,signal:AbortSignal,snapshotVersion?:number,parentID?:string,departmentID?:string)=>platformRequest<SelectionPage>(`${path(w,a)}/selectors/${kind}?${new URLSearchParams({provider,q,limit:'20',...(kind==='groups'?{}:{purpose}),...(cursor?{cursor}:{}),...(snapshotVersion?{snapshot_version:String(snapshotVersion)}:{}),...(parentID!==undefined?{parent_id:parentID}:{}),...(departmentID?{department_id:departmentID}:{})})}`,{signal}),
 resolve:(w:string,a:string,kind:'people'|'groups',refs:SelectionRef[],purpose:SelectorPurpose,signal:AbortSignal)=>platformRequest<{items:SelectionCandidate[]}>(`${path(w,a)}/selectors/${kind}/resolve`,{...post({... (kind==='groups'?{}:{purpose}),refs}),signal}),
 refreshWecom:(w:string,a:string,signal:AbortSignal)=>platformRequest<{job_id:string;status:string}>(`${path(w,a)}/channels/wecom/groups/refresh`,{...post({}),signal}),
 wecomRefreshStatus:(w:string,a:string,id:string,signal:AbortSignal)=>platformRequest<{job_id:string;status:string;error_code?:string}>(`${path(w,a)}/channels/wecom/groups/refresh/${encodeURIComponent(id)}`,{signal})
};
