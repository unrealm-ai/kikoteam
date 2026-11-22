import type {UsageSelector} from './agent-usage';
import { platformRequest, post } from './platform';
export const workspacePath = (workspace: string) => `/workspaces/${encodeURIComponent(workspace)}`;
const segment = encodeURIComponent;
export interface Page<T> { items: T[]; total: number; page: number; page_size: number }
export interface AssetManifest { format_version: number; files: { path: string; size: number; sha256: string }[]; digest: string }
export interface SkillVersion { id: string; sequence: number; version_label: string; technical_name: string; description: string; manifest: AssetManifest; source: Record<string, string>; created_at: string }
export interface ResourceCardCopy { category: string; summary_zh: string; summary_en: string; highlights: string }
export interface CatalogEntry { card?: ResourceCardCopy; scope?: "workspace"|"official"; listed?: boolean; assigned_agent_count?: number }
export interface Skill extends CatalogEntry { id: string; name: string; description: string; enabled: boolean; version: number; current_version_id: string; current_version: SkillVersion; created_at: string; updated_at: string }
export interface SkillImport { id: string; technical_name: string; description: string; manifest: AssetManifest; source: Record<string,string>; expires_at: string }
export interface SkillImportInput { source: 'zip'|'github'|'markdown'|'manual'; zip_base64?: string; markdown?: string; repository_url?: string; ref?: string; subpath?: string; name?: string; description?: string; body?: string }
const skillPath = (w:string,id?:string) => `${workspacePath(w)}/skills${id ? `/${segment(id)}` : ''}`;
export const skillsApi = {
 get: (w:string,id:string,signal:AbortSignal) => platformRequest<Skill>(skillPath(w,id), {signal}),
 list: (w:string,page=1,signal?:AbortSignal,scope="all",query="") => platformRequest<Page<Skill>>(`${scope==="manage"?"/official-skills":skillPath(w)}?page=${page}&page_size=20${scope==="manage"?"":`&scope=${scope}`}${query ? `&q=${segment(query)}` : ""}`, signal ? {signal}:{}),
 import: (w:string,input:SkillImportInput,signal:AbortSignal) => platformRequest<SkillImport>(`${workspacePath(w)}/skill-imports`, {...post(input),signal}),
 publish: (w:string, importID:string, name:string,label:string, existing?:Skill) => platformRequest<Skill>(existing ? `${skillPath(w,existing.id)}/versions` : skillPath(w),post({import_id:importID,version_label:label,...(existing?{version:existing.version}:{name})})),
 update: (w:string,item:Skill,change:{name?:string; enabled?:boolean}) => platformRequest<Skill>(skillPath(w,item.id),{method:'PATCH',body:JSON.stringify({version:item.version,...change})}),
 versions: async (w:string,id:string) => ({items:await allPages(n=>platformRequest<Page<SkillVersion>>(`${skillPath(w,id)}/versions?page=${n}&page_size=100`))}),
 file: (w:string,id:string,version:string,path:string) => platformRequest<{path:string;content_base64:string;size:number;sha256:string}>(`${skillPath(w,id)}/versions/${segment(version)}/files?path=${segment(path)}`)
};
export type AuthType = 'none'|'bearer'|'api_key'|'oauth';
export interface OAuthConfig {issuer:string;client_id:string;client_secret?:string;resource?:string;scopes:string}
export interface PluginConfig { oauth?:OAuthConfig; name:string; description:string; endpoint:string; auth_type:AuthType; header_name:string }
export interface Plugin extends PluginConfig, CatalogEntry {supported_auth_methods?:AuthType[];default_auth_method?:AuthType;definition_version:number;shared_connection_state?:"missing"|"connected"|"unvalidated"|"revoked";id:string;enabled:boolean;version:number;version_id:string;created_at:string;updated_at:string}
export interface PluginTool {name:string;description:string;input_schema:Record<string,unknown>;schema_digest:string}
export type ConnectionOwner = "workspace"|"agent";
export interface ConnectionSource {source:ConnectionOwner;revision:number;account?:PluginAccount}
export interface PluginAccount {auth_method?:AuthType;agent_count?:number;external_account?:string;granted_scopes?:string;source?:ConnectionOwner;id:string;plugin_id:string;plugin_version_id:string;name:string;version:number;enabled:boolean;validated:boolean;tools:PluginTool[];validated_at:string}
const pluginPath=(w:string,id?:string)=>`${workspacePath(w)}/plugins${id?`/${segment(id)}`:''}`;
const accountPath=(w:string,a:string,p:string)=>`${workspacePath(w)}${a?`/agents/${segment(a)}`:""}/plugin-accounts/${segment(p)}`;
const sourcePath=(w:string,a:string,p:string,v:string)=>`${workspacePath(w)}/agents/${segment(a)}/plugins/${segment(p)}/connection-source?plugin_version_id=${segment(v)}`;
export const pluginsApi={
 get:(w:string,id:string,signal:AbortSignal)=>platformRequest<Plugin>(pluginPath(w,id),{signal}),
 shared:(w:string)=>platformRequest<{items:PluginAccount[]}>(workspacePath(w)+"/plugin-accounts"),
 source:(w:string,a:string,p:string,v:string)=>platformRequest<ConnectionSource>(sourcePath(w,a,p,v)),
 setSource:(w:string,a:string,p:string,v:string,source:ConnectionOwner,revision:number)=>platformRequest<ConnectionSource>(sourcePath(w,a,p,v),{method:'PUT',body:JSON.stringify({source,revision})}),
 authorize:(w:string,a:string,p:string,name:string,version:number,plugin_version_id:string,tenant_oauth?:{resource:string,issuer:string,client_id:string,client_secret:string,scopes?:string,read_tools:string[]})=>platformRequest<{authorization_url:string}>(accountPath(w,a,p)+"/oauth/authorize",post({name,version,plugin_version_id,tenant_oauth})),
 list:(w:string,page=1,signal?:AbortSignal,scope="all",query="")=>platformRequest<Page<Plugin>>(`${scope==="manage"?"/official-plugins":pluginPath(w)}?page=${page}&page_size=20${scope==="manage"?"":`&scope=${scope}`}${query ? `&q=${segment(query)}` : ""}`,signal?{signal}:{}),
 save:(w:string,config:PluginConfig,existing?:Plugin)=>platformRequest<Plugin>(pluginPath(w,existing?.id),existing?{method:'PATCH',body:JSON.stringify({...config,version:existing.version})}:post(config)),
 status:(w:string,p:Plugin)=>platformRequest<Plugin>(pluginPath(w,p.id),{method:'PATCH',body:JSON.stringify({version:p.version,enabled:!p.enabled})}),
 versions:async(w:string,id:string)=>({items:await allVersions(w,id)}),
 account:(w:string,a:string,p:string,v?:string)=>platformRequest<PluginAccount>(accountPath(w,a,p)+(v?`?plugin_version_id=${segment(v)}`:"")),
 saveAccount:(w:string,a:string,p:string,body:{plugin_version_id:string;name:string;version:number;secret?:string;auth_method?:AuthType},signal:AbortSignal)=>platformRequest<PluginAccount>(accountPath(w,a,p),{method:'PUT',body:JSON.stringify(body),signal}),
 revokeAccount:(w:string,a:string,p:string,version:number,v?:string)=>platformRequest<void>(accountPath(w,a,p)+(v?`?plugin_version_id=${segment(v)}`:""),{method:'DELETE',body:JSON.stringify({version})})
};
export interface PackageScope {mode:'inherit_with_blacklist'|'whitelist_with_blacklist';allow_member_ids:string[];deny_member_ids:string[];allow?:UsageSelector[];deny?:UsageSelector[]}
export interface PackageSkill {skill_id:string;version_id:string}
export interface PackagePlugin {plugin_id:string;version_id:string}
export interface PackageTriggerScope {enabled?:boolean;mode:'all'|'allowlist';allow_trigger_ids:string[];deny_trigger_ids:string[]}
export const defaultTriggerScope=():PackageTriggerScope=>({mode:'all',allow_trigger_ids:[],deny_trigger_ids:[]});
export interface PackageSceneScope {enabled:boolean;mode:'inherit_with_blacklist'|'allowlist';allow:UsageSelector[];deny:UsageSelector[]}
export interface PackageConversationScope {private:PackageSceneScope;group:PackageSceneScope}
export const defaultConversationScope=(enabled = false):PackageConversationScope=>({private:{enabled,mode:'inherit_with_blacklist',allow:[],deny:[]},group:{enabled,mode:'inherit_with_blacklist',allow:[],deny:[]}});
export interface PackageInput {work_scope?:import("./work-items").WorkScope;conversation_scope?:PackageConversationScope|null;name:string;description:string;enabled:boolean;scope:PackageScope;trigger_scope:PackageTriggerScope;skills:PackageSkill[];plugins:PackagePlugin[]}
export interface CapabilityPackage extends PackageInput {id:string;agent_id?:string;source_template_id?:string;version:number;created_at:string;updated_at:string}
const packagePath=(w:string,a:string,id?:string)=>`${workspacePath(w)}/${a?`agents/${segment(a)}/capability-packages`:'capability-package-templates'}${id?`/${segment(id)}`:''}`;
export const packagesApi={
 get:(w:string,a:string,id:string)=>platformRequest<CapabilityPackage>(packagePath(w,a,id)),
 upgrade:(w:string,a:string,kind:'skill'|'plugin',resource_id:string,from_version_id:string,to_version_id:string)=>platformRequest<{upgraded:boolean}>(workspacePath(w)+'/agents/'+segment(a)+'/capability-upgrades',post({kind,resource_id,from_version_id,to_version_id})),
 list:(w:string,a:string,page=1,signal?:AbortSignal)=>platformRequest<Page<CapabilityPackage>>(`${packagePath(w,a)}?page=${page}&page_size=20`,signal?{signal}:{}),
 save:(w:string,a:string,input:PackageInput,existing?:CapabilityPackage)=>{
  // Legacy and scene scopes are distinct authorization models. Never submit both.
  const fields=Object.fromEntries(Object.entries(input).filter(([key])=>key!=='scope'||!input.conversation_scope));
  return platformRequest<CapabilityPackage>(packagePath(w,a,existing?.id),existing?{method:'PATCH',body:JSON.stringify({...Object.fromEntries(Object.entries(fields).filter(([key,value])=>JSON.stringify(value)!==JSON.stringify(existing[key as keyof PackageInput]))),version:existing.version})}:post(fields));
 },
 copy:(w:string,a:string,template:CapabilityPackage)=>platformRequest<CapabilityPackage>(`${workspacePath(w)}/agents/${segment(a)}/capability-package-copies`,post({template_id:template.id,template_version:template.version}))
};
export async function allPages<T extends {id:string}>(get:(page:number)=>Promise<Page<T>>):Promise<T[]> {const all:T[]=[];const seen=new Set<string>();for(let page=1;page<=1000;page++){const r=await get(page);if(r.page!==page||r.items.some(i=>seen.has(i.id)))throw new Error('分页响应异常，请重试。');for(const i of r.items){seen.add(i.id);all.push(i);}if(!r.items.length||all.length>=r.total)return all;}throw new Error('资源数量过多，请缩小范围。');}

async function allVersions(w:string,id:string):Promise<Plugin[]> {const all:Plugin[]=[];const seen=new Set<string>();for(let page=1;page<=1000;page++){const r=await platformRequest<Page<Plugin>>(`${pluginPath(w,id)}/versions?page=${page}&page_size=100`);if(r.page!==page||r.items.some(i=>seen.has(i.version_id)))throw new Error('插件版本分页异常');for(const i of r.items){seen.add(i.version_id);all.push(i);}if(!r.items.length||all.length>=r.total)return all;}throw new Error('插件版本过多');}
