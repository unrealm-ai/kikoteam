import {platformRequest,post} from './platform';
import type {SelectionRef} from './selectors';
export interface SharingDraft {name:string;mode:'directed'|'mutual';enabled:boolean;sources:SelectionRef[];targets:SelectionRef[]}
export interface SharingPolicy extends SharingDraft {id:string;workspace_id:string;agent_id:string;version:number;created_at:string;updated_at:string}
export interface SharingEdge {source:SelectionRef;target:SelectionRef;history_capability:string;unavailable_reason?:string}
const path=(w:string,a:string)=>`/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/sharing-policies`;
export const sharingApi={
 list:(w:string,a:string,q:string,page:number,signal:AbortSignal)=>platformRequest<{items:SharingPolicy[];total:number}>(`${path(w,a)}?${new URLSearchParams({q,page:String(page),page_size:'20'})}`,{signal}),
 save:(w:string,a:string,draft:SharingDraft,id:string|undefined,version:number|undefined,signal:AbortSignal)=>platformRequest<SharingPolicy>(`${path(w,a)}${id?'/'+encodeURIComponent(id):''}`,{method:id?'PUT':'POST',body:JSON.stringify({...draft,...(id?{version}:{})}),signal}),
 remove:(w:string,a:string,id:string,version:number,signal:AbortSignal)=>platformRequest<void>(`${path(w,a)}/${encodeURIComponent(id)}?version=${version}`,{method:'DELETE',signal}),
 preview:(w:string,a:string,draft:SharingDraft,signal:AbortSignal)=>platformRequest<{edges:SharingEdge[]}>(`${path(w,a)}/preview`,{...post(draft),signal})
};
