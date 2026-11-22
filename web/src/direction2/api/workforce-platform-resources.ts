import { allPages, skillsApi, pluginsApi, packagesApi, type Skill, type Plugin, type CapabilityPackage, type Page } from './capabilities';
import { modelProfilesApi, choiceValue, type ModelChoice } from './models';
import { platformRequest } from './platform';
import { selectorsApi, type SelectorPurpose } from './selectors';
import { activeWorkspaceId } from './client';
import type { ModelItem, WorkspaceSkill, McpServerEndpoint, WorkspaceEmailInvitation } from '../types';

/** Resource selectors use only facts returned by Platform. Null means not
 * exposed by this API, not zero/false or a successful discovery. These are not
 * writable legacy resource DTOs. */
export type WorkforceModelResource = Omit<ModelItem, 'modelCode'|'endpoint'|'apiKeyMasked'|'boundEmployeesCount'|'supportsThinking'> & {
 modelCode:string|null;endpoint:string|null;apiKeyMasked:string|null;boundEmployeesCount:null;supportsThinking:null;
 selectable:boolean;platformResource:ModelChoice;
};
export type WorkforceSkillResource = Omit<WorkspaceSkill,'promptTemplate'|'requiredToolTags'|'boundEmployeesCount'|'historyVersions'|'source'> & {
 source:WorkspaceSkill['source']|null;promptTemplate:null;requiredToolTags:null;boundEmployeesCount:null;historyVersions:null;
 versionId:string;platformResource:Skill;
};
export type WorkforcePluginResource = Pick<McpServerEndpoint,'id'|'name'|'endpoint'|'authType'|'enabled'> & {
 protocol:'Streamable HTTP';status:null;tools:null;allowedTools:null;whitelistMatched:null;
 accountScope:'employee';platformResource:Plugin;
};
export interface PlatformEmailInvitation {
 id:string;kind:'email'|'link';email?:string;role:string;status:string;expires_at:string;delivery_status:string;contact_email:string;
}
export type WorkforceInvitationResource = Omit<WorkspaceEmailInvitation,'name'|'targetEmployeeIds'|'invitedAt'|'status'> & {
 name:null;targetEmployeeIds:null;invitedAt:null;status:WorkspaceEmailInvitation['status']|null;
 scope:'workspace';platformResource:PlatformEmailInvitation;
};

export function modelResourceForWorkforce(raw:ModelChoice):WorkforceModelResource {
 const workspace=raw.source!=='platform'?raw:null;
 return {id:choiceValue(raw),name:raw.name,protocol:raw.protocol,modelCode:workspace?.model_id??null,
 endpoint:workspace?.base_url??null,apiKeyMasked:workspace?workspace.has_key?'••••':null:null,
 boundEmployeesCount:null,supportsThinking:null,enabled:raw.enabled,isDefault:raw.is_default,
 selectable:raw.enabled&&raw.validated===true,platformResource:structuredClone(raw)};
}
export function skillResourceForWorkforce(raw:Skill):WorkforceSkillResource {
 const source=raw.current_version.source;
 const sourceKind:Record<string,WorkspaceSkill['source']>={manual:'local_import',markdown:'markdown_file',zip:'zip_archive',github:'git_synced'};
 return {id:raw.id,name:raw.name,code:raw.current_version.technical_name,version:raw.current_version.version_label,
 versionId:raw.current_version_id,source:sourceKind[source.type||'']??null,description:raw.description,enabled:raw.enabled,
 createdAt:raw.created_at,promptTemplate:null,requiredToolTags:null,boundEmployeesCount:null,historyVersions:null,
 ...(source.repository_url?{gitRepoUrl:source.repository_url}:{}),...(source.ref?{gitBranch:source.ref}:{}),...(source.commit?{gitCommit:source.commit}:{}),
 platformResource:structuredClone(raw)};
}
export function pluginResourceForWorkforce(raw:Plugin):WorkforcePluginResource {
 // Platform plugin endpoints implement Streamable HTTP; authentication and tool
 // discovery belong to a specific employee account, not this workspace row.
 return {id:raw.id,name:raw.name,endpoint:raw.endpoint,authType:raw.auth_type==='none'?'no_auth':raw.auth_type==='bearer'?'bearer_token':'api_key',
 enabled:raw.enabled,protocol:'Streamable HTTP',status:null,tools:null,allowedTools:null,whitelistMatched:null,accountScope:'employee',platformResource:structuredClone(raw)};
}
export function invitationResourceForWorkforce(raw:PlatformEmailInvitation):WorkforceInvitationResource {
 if(raw.kind!=='email'||!raw.email)throw new Error('不是邮箱邀请');
 const states:Record<string,WorkspaceEmailInvitation['status']>={valid:'pending',pending:'pending',accepted:'activated',expired:'expired',revoked:'revoked'};
 return {id:raw.id,email:raw.email,name:null,targetEmployeeIds:null,invitedAt:null,expiresAt:raw.expires_at,
 status:states[raw.status]??null,scope:'workspace',platformResource:structuredClone(raw)};
}

/** Model choices have two namespaces: workspace and platform UUIDs can be equal.
 * Detect duplicate composite IDs rather than incorrectly rejecting that case. */
async function allModelChoices(w:string,signal?:AbortSignal):Promise<ModelChoice[]> {
 const out:ModelChoice[]=[];const seen=new Set<string>();
 for(let page=1;page<=1000;page++){
  const r=await modelProfilesApi.choices(w,page,signal);
  if(r.page!==page)throw new Error('模型分页响应异常');
  for(const item of r.items){const key=choiceValue(item);if(seen.has(key))throw new Error('模型分页重复');seen.add(key);out.push(item);}
  if(out.length>=r.total)return out;
  if(!r.items.length)throw new Error('模型分页提前结束');
 }
 throw new Error('模型资源过多');
}
function employeeScope(a?:string):string {if(!a)throw new Error('人员和群候选需要先选择 AI 员工，不能读取工作区伪全量目录');return a;}
function requestSignal(signal?:AbortSignal):AbortSignal{return signal??AbortSignal.timeout(20000);}

export const workforcePlatformResources = {
 listModels:async(w=activeWorkspaceId.value,signal?:AbortSignal):Promise<WorkforceModelResource[]> => (await allModelChoices(w,signal)).map(modelResourceForWorkforce),
 listSkills:async(w=activeWorkspaceId.value,signal?:AbortSignal):Promise<WorkforceSkillResource[]> => (await allPages(n=>skillsApi.list(w,n,signal))).map(skillResourceForWorkforce),
 listSkillVersions:(w:string,skillId:string)=>skillsApi.versions(w,skillId),
 getEmployeePluginAccount:(w:string,a:string,pluginId:string)=>pluginsApi.account(w,employeeScope(a),pluginId),
 listMcpServers:async(w=activeWorkspaceId.value,signal?:AbortSignal):Promise<WorkforcePluginResource[]> => (await allPages(n=>pluginsApi.list(w,n,signal))).map(pluginResourceForWorkforce),
 // Template rows remain raw: they are not yet employee-bound packages, nor a
 // promise of inherited handoff/conversation-kind/bash permissions.
 getCapabilityPackagesCatalog:async(w=activeWorkspaceId.value,signal?:AbortSignal):Promise<CapabilityPackage[]> => allPages(n=>packagesApi.list(w,'',n,signal)),
 listEmailInvitations:async(w=activeWorkspaceId.value,signal?:AbortSignal):Promise<WorkforceInvitationResource[]> => {
  const rows=await allPages(n=>platformRequest<Page<PlatformEmailInvitation>>(`/workspaces/${encodeURIComponent(w)}/invitations?page=${n}&page_size=100`,signal?{signal}:{}));
  return rows.filter(r=>r.kind==='email').map(invitationResourceForWorkforce);
 },
 // The original sync aggregate cannot represent employee identity epochs and
 // per-purpose availability. Expose the real sources and let the picker fetch
 // one page on demand; do not materialize an empty "synced" aggregate.
 getDirectorySyncSources:(w=activeWorkspaceId.value,a?:string,purpose:SelectorPurpose='usage',signal?:AbortSignal)=>selectorsApi.sources(w,employeeScope(a),purpose,requestSignal(signal)),
 getOptions:(w=activeWorkspaceId.value,a?:string,purpose:SelectorPurpose='usage',signal?:AbortSignal)=>selectorsApi.sources(w,employeeScope(a),purpose,requestSignal(signal))
};
