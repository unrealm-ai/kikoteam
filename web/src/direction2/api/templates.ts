import {platformRequest,post} from './platform';
import type {Agent} from './agents';
import type {Page,CapabilityPackage} from './capabilities';
export interface Parameter{key:string;label:string;description:string;type:'short_text'|'long_text'|'single_select'|'multi_select';required:boolean;default?:unknown;options?:string[];max_length?:number}
export interface Resource{resource_key:string;kind:'skill'|'plugin';workspace_id?:string;id?:string;version_id?:string}
export interface PresetPackage{package_key:string;name:string;description:string;onboarding_requirement:'required'|'deferred';skills:{resource_key:string}[];plugins:{resource_key:string}[]}
export interface Example{can_try?:boolean;key:string;title:string;text:string;output:string;package_keys:string[]}
export interface Content{schema_version:number;name:string;avatar:string;role:string;category:string;audience:string;deliverables:string;model_requirements:string;instruction_template:string;parameters:Parameter[];packages:PresetPackage[];resource_refs:Resource[];examples:Example[]}
export interface AgentTemplate{builtin_key?:string;id:string;scope:string;version:number;content:Content;sequence:number;template_version_id:string;latest_version_id:string;published_version_id:string;has_unpublished_changes:boolean;is_listed:boolean;recommended_rank:number;status:string;published_at:string|null}
export interface TemplateVersion{id:string;template_id:string;sequence:number;content:Content;change_note:string;published_at:string|null}
export interface Setup{conversation_model:{available:boolean};task_model:{available:boolean};access_allowed:boolean;packages:CapabilityPackage[];issues:{package_id:string;resource_id:string;code:string}[];examples:Example[]}
const base=(workspace:string,official:boolean)=>official?'/official-agent-templates':`/workspaces/${encodeURIComponent(workspace)}/agent-templates`;
export const templatesApi={
 list:(w:string,o:boolean,query:URLSearchParams,signal:AbortSignal)=>platformRequest<Page<AgentTemplate>>(`${base(w,o)}?${query}`,{signal}),
 get:(w:string,o:boolean,id:string,manage=false)=>platformRequest<AgentTemplate>(`${base(w,o)}/${id}${manage?'?view=manage':''}`),
 create:(w:string,o:boolean,content:Content)=>platformRequest<AgentTemplate>(base(w,o),post({content})),
 update:(w:string,o:boolean,t:AgentTemplate,change:Record<string,unknown>)=>platformRequest<AgentTemplate>(`${base(w,o)}/${t.id}`,{method:'PATCH',body:JSON.stringify({version:t.version,...change})}),
 publish:(w:string,o:boolean,t:AgentTemplate)=>platformRequest<AgentTemplate>(`${base(w,o)}/${t.id}/publish`,post({version:t.version,template_version_id:t.latest_version_id})),
 remove:(w:string,o:boolean,t:AgentTemplate)=>platformRequest<void>(`${base(w,o)}/${t.id}`,{method:'DELETE',body:JSON.stringify({version:t.version})}),
 versions:(w:string,o:boolean,id:string)=>platformRequest<Page<TemplateVersion>>(`${base(w,o)}/${id}/versions?view=manage&page_size=100`),
 fromAgent:(w:string,a:Agent)=>platformRequest<AgentTemplate>(`/workspaces/${w}/agents/${a.id}/agent-templates`,post({agent_version:a.version,config_version:a.config_version,name:a.name})),
 createAgent:(w:string,body:Record<string,unknown>,signal:AbortSignal)=>platformRequest<Agent>(`/workspaces/${w}/agents/from-template`,{...post(body),signal}),
 setup:(w:string,id:string)=>platformRequest<Setup>(`/workspaces/${w}/agents/${id}/setup-status`)
};
export const emptyContent=():Content=>({schema_version:1,name:'',avatar:'cn-v2-01',role:'',category:'office',audience:'',deliverables:'',model_requirements:'',instruction_template:'',parameters:[],packages:[],resource_refs:[],examples:[]});
export function renderInstruction(content:Content,parameters:Record<string,unknown>){return content.instruction_template.replace(/\{\{\s*([a-z][a-z0-9_]*)\s*\}\}/g,(token,key:string)=>{const value=parameters[key];return value===undefined?token:Array.isArray(value)?value.join('、'):String(value)});}
