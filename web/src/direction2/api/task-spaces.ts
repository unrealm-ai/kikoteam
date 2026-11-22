import { platformRequest, post } from './platform';
import type { Page, ManagerSession } from './manager';
export interface TaskSpace { id:string;workspace_id:string;business_key:string|null;created_by:string;lifecycle:string;version:string;created_at:string;agents:{agent_id:string;status:string;admission_epoch:string}[] }
export interface SpaceCreate { business_key:string|null;agent_id:string }
export interface SpaceTaskCreate { task_space_id:string;agent_id:string;mode:'task';title:string|null;input:{text:string} }
const base=(w:string)=>`/workspaces/${encodeURIComponent(w)}/task-spaces`;
export const taskSpacesApi={
 list:(w:string,cursor:string,signal:AbortSignal)=>platformRequest<Page<TaskSpace>>(`${base(w)}?${new URLSearchParams({limit:'20',...(cursor?{cursor}:{})})}`,{signal}),
 get:(w:string,id:string,signal:AbortSignal)=>platformRequest<TaskSpace>(`${base(w)}/${encodeURIComponent(id)}`,{signal}),
 create:(w:string,body:SpaceCreate,key:string,signal:AbortSignal)=>platformRequest<TaskSpace>(base(w),{...post(body),headers:{'Idempotency-Key':key},signal}),
 grant:(w:string,id:string,agent:string,version:string,enabled:boolean,signal:AbortSignal)=>platformRequest<TaskSpace>(`${base(w)}/${encodeURIComponent(id)}/agents/${encodeURIComponent(agent)}`,{method:'PUT',body:JSON.stringify({version,enabled}),signal}),
 createTask:(w:string,body:SpaceTaskCreate,key:string,signal:AbortSignal)=>platformRequest<ManagerSession>(`/workspaces/${encodeURIComponent(w)}/sessions`,{...post(body),headers:{'Idempotency-Key':key},signal})
};
