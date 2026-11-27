import type {CapabilityPackageBinding,DigitalEmployee,EmployeeTrigger,PrincipalRef,TriggerAllowedAction} from '../types';
import {buildCanonicalPrincipalKey} from '../types';
import {agentsApi,type Agent,type AgentDirectoryFilter} from './agents';
import type {UsageResource, UsageSelector} from './agent-usage';
import {allPages,packagesApi,type CapabilityPackage} from './capabilities';
import {channelsApi,type ChannelProvider} from './channels';
import type {FeishuBinding} from './feishu';
import {triggersApi,type Trigger,type TriggerTarget} from './triggers';
import type {WorkforceTriggerRules} from './workforce-trigger-rules';
import {bindingForWorkforce} from './workforce-channel-binding';
import {D2ApiError} from './client';

/** Presentation is not a write payload. Raw resources retain independent CAS revisions. */
export interface WorkforcePlatformSnapshot {
 employee:DigitalEmployee; agent:Agent; usage?:UsageResource;
 bindings:Partial<Record<ChannelProvider,FeishuBinding>>; rules?:WorkforceTriggerRules;
 packages:CapabilityPackage[]; triggers:Trigger[]; targets:TriggerTarget[];
 availability:{model:boolean;statistics:false;thinkingLevel:false;handoff:false;humanConfirmation:false;triggerLogs:false;sharing:false};
 errors:{resource:string;message:string}[];
}
const cache=new Map<string,WorkforcePlatformSnapshot>();
const generations=new Map<string,number>();
const key=(w:string,a:string)=>JSON.stringify([w,a]);
export const workforceChannelProviders = ['feishu', 'dingtalk', 'wecom'] as const;

export async function readWorkforceChannel(w: string, a: string, provider: ChannelProvider, signal?: AbortSignal) {
 const timeout = AbortSignal.timeout(20000);
 try {
  const binding = await channelsApi.get(w, a, provider, signal ? AbortSignal.any([signal, timeout]) : timeout);
  signal?.throwIfAborted();
  return {binding};
 } catch (error) {
  signal?.throwIfAborted();
  if (error instanceof D2ApiError && error.status === 404 && error.code === 'channel_not_configured') return {binding: null};
  return {error: timeout.aborted ? '读取渠道超时' : error instanceof Error ? error.message || '读取渠道失败' : '读取渠道失败'};
 }
}

export function getWorkforcePlatformSnapshot(w:string,a:string):WorkforcePlatformSnapshot|undefined {const value=cache.get(key(w,a));return value?structuredClone(value):undefined;}
export function invalidateWorkforcePlatformSnapshot(w:string,a?:string):void {
 const keys=a===undefined?[...new Set([...cache.keys(),...generations.keys()])].filter(k=>JSON.parse(k)[0]===w):[key(w,a)];
 for(const k of keys){cache.delete(k);generations.set(k,(generations.get(k)||0)+1);}
}
export type WorkforcePrincipalRef = PrincipalRef & {usageSelector:UsageSelector};
function principal(v:UsageSelector):WorkforcePrincipalRef|null {
 if(v.kind==='group')return null;
 const ref:WorkforcePrincipalRef={channel:v.channel,targetType:v.kind,targetId:v.id,connectionId:v.binding_id||'',usageSelector:structuredClone(v)};
 ref.canonicalKey=buildCanonicalPrincipalKey(ref);return ref;
}
const principals=(values:UsageSelector[])=>values.flatMap(v=>{const p=principal(v);return p?[p]:[];});
const webIDs=(values:UsageSelector[])=>values.filter(v=>v.channel==='web'&&v.kind==='user').map(v=>v.id);
const unloadedAccess: UsageResource['usage_config']['access'] = {mode:'allowlist',group_members_enabled:false,allow:[],deny:[]};
function projectWorkforceAccess(access: UsageResource['usage_config']['access']) {
 const allowed = principals(access.allow), denied = principals(access.deny);
 return {
  unifiedAccess: {mode:access.mode==='all'?'all_with_blacklist':'whitelist_with_blacklist',whitelistMemberIds:allowed.map(p=>p.canonicalKey!),blacklistMemberIds:denied.map(p=>p.canonicalKey!),whitelistTargets:allowed,blacklistTargets:denied,allowGroupMembers:access.group_members_enabled},
  webChatAccess: {mode:access.mode==='all'?'all':'custom',allowMemberIds:webIDs(access.allow),denyMemberIds:webIDs(access.deny),allowTargets:principals(access.allow.filter(v=>v.channel==='web')),denyTargets:principals(access.deny.filter(v=>v.channel==='web'))},
 } satisfies Pick<DigitalEmployee,'unifiedAccess'|'webChatAccess'>;
}

export function acceptWorkforceUsageRead(w: string, a: string, usage: UsageResource) {
 const current = cache.get(key(w,a));
 if (!current) throw new Error('请重新加载员工配置');
 if (!current.usage || usage.version >= current.usage.version) {
  current.usage = usage;
  Object.assign(current.employee, projectWorkforceAccess(usage.usage_config.access));
  Object.assign(current.employee.resourceVersions!, {usageAccess:usage.version,usageHistory:usage.version});
 }
 return structuredClone(current.employee);
}
export function packageForWorkforce(p:CapabilityPackage):CapabilityPackageBinding {
 return {id:p.id,name:p.name,description:p.description,enabled:p.enabled,isBound:true,
  resources:{skills:p.skills.map(s=>s.skill_id),skillVersionPins:Object.fromEntries(p.skills.map(s=>[s.skill_id,s.version_id])),mcp:p.plugins.map(x=>x.plugin_id),mcpTools:[],bash:false},
  scope:{userScope:{mode:p.scope.mode,whitelistMemberIds:[...p.scope.allow_member_ids],blacklistMemberIds:[...p.scope.deny_member_ids],whitelistTargets:principals(p.scope.allow||[]),blacklistTargets:principals(p.scope.deny||[])},
   // Unsupported legacy controls must stay disabled. Never round-trip these defaults.
   channelPolicies:[],conversationKinds:[],triggerUsageScope:{mode:p.trigger_scope.mode,allowTriggerIds:[...p.trigger_scope.allow_trigger_ids],denyTriggerIds:[...p.trigger_scope.deny_trigger_ids]},handoffUsageScope:{mode:'inherit_with_blacklist',allowEmployeeIds:[],denyEmployeeIds:[]}}};
}
export function triggerForWorkforce(t:Trigger,targets:TriggerTarget[]):EmployeeTrigger {
 const business=t.config.businessKey;const allowed=new Set<TriggerAllowedAction>(['create_task','supplement_task','cancel_task']);
 return {id:t.id,name:t.name,type:t.kind,enabled:t.enabled,allowedActions:t.allowed_actions.filter((a):a is TriggerAllowedAction=>allowed.has(a as TriggerAllowedAction)),cron:t.config.cron,timezone:t.config.timezone,description:t.config.description||'',businessKeyMode:business?.mode,businessKeyPattern:business?.mode==='fixed'?business.value:business?.period,nextExecutionTimes:t.next_due?[t.next_due]:[],historyPolicy:business&&business.mode!=='empty'?'business_key':'isolated',
 notificationTargets:t.notification_targets.flatMap(ref=>{const row=targets.find(x=>x.conversation_id===ref.conversation_id);return row&&['feishu','dingtalk','wecom'].includes(row.provider)?[{platform:row.provider as ChannelProvider,groupName:row.label}]:[];}),executionLogs:[]};
}
export function projectWorkforceSnapshot(raw:Omit<WorkforcePlatformSnapshot,'employee'|'availability'|'errors'>):WorkforcePlatformSnapshot {
 const {agent,usage,bindings,rules,packages,triggers,targets}=raw;
 const channels=Object.values(bindings).map(binding=>{const value=bindingForWorkforce(binding,usage);if(binding.provider==='feishu'&&rules)value.capabilities.feishuRules=rules.rules.filter(r=>r.channels.includes('feishu')).map(({channels:_channels,...r})=>r);return value;});
 return {...structuredClone(raw),employee:{canManage:agent.can_manage,canChat:agent.can_chat,configVersion:agent.config_version,modelAvailable:agent.model_available,id:agent.id,code:agent.id,version:agent.version,resourceVersions:{...(usage ? {usageAccess:usage.version,usageHistory:usage.version} : {}),...(rules ? {triggerRules:rules.version} : {}),channels:Object.fromEntries(Object.values(bindings).map(b=>[b.provider,b.config_revision]))},name:agent.name,role:agent.role,avatar:agent.avatar,status:agent.enabled?'active':'disabled',executionEngine:agent.harness,modelId:agent.platform_model_id?`platform:${agent.platform_model_id}`:`workspace:${agent.model_profile_id}`,modelName:agent.model_name,thinkingLevel:null,prompt:agent.instruction,
 ...projectWorkforceAccess(usage?.usage_config.access ?? unloadedAccess),channels,capabilityPackages:packages.map(packageForWorkforce),triggers:triggers.map(t=>triggerForWorkforce(t,targets)),humanConfirmationRules:[],
 handoffPolicy:{mode:'whitelist',allowEmployeeIds:[],denyEmployeeIds:[]},stats:{todayTasks:null,todayConversations:null,successRate:null}},
 availability:{model:agent.model_available,statistics:false,thinkingLevel:false,handoff:false,humanConfirmation:false,triggerLogs:false,sharing:false},errors:[]};
}
export async function getWorkforcePlatformEmployee(w:string,a:string,signal?:AbortSignal,policy?:Pick<WorkforcePlatformSnapshot,'usage'|'rules'>):Promise<DigitalEmployee> {
 const k=key(w,a),epoch=(generations.get(k)||0)+1;generations.set(k,epoch);cache.delete(k);
 const s=signal?AbortSignal.any([signal,AbortSignal.timeout(20000)]):AbortSignal.timeout(20000);
 const agent = await agentsApi.get(w,a,s);
 if(signal?.aborted||generations.get(k)!==epoch)throw new DOMException('员工读取已被新的请求取代','AbortError');
 if(agent.can_manage===false) return workforceAgentSummary(agent);
 const [packages,triggers,targets,entries]=await Promise.all([
  allPages(n=>packagesApi.list(w,a,n,s)),triggersApi.list(w,a,s),triggersApi.targets(w,a,s),
  Promise.all(workforceChannelProviders.map(async provider => [provider, await readWorkforceChannel(w,a,provider,signal)] as const)),
 ]);
 if(signal?.aborted||generations.get(k)!==epoch)throw new DOMException('员工读取已被新的请求取代','AbortError');
 const bindings:Partial<Record<ChannelProvider,FeishuBinding>>={};
 const channelReadErrors: NonNullable<DigitalEmployee['channelReadErrors']> = {};
 for (const [provider, result] of entries) {
  if (result.error !== undefined) channelReadErrors[provider] = result.error;
  else if (result.binding) bindings[provider] = result.binding;
 }
 const snapshot=projectWorkforceSnapshot({agent,...policy,packages,triggers:triggers.items,targets:targets.items,bindings});
 if (Object.keys(channelReadErrors).length) snapshot.employee.channelReadErrors = channelReadErrors;
 if(targets.truncated)snapshot.errors.push({resource:'notificationTargets',message:'通知目标列表未完整加载，不能据此删除缺失目标。'});
 cache.set(k,snapshot);return structuredClone(snapshot.employee);
}
/** Directory summaries and write receipts do not establish a full employee configuration snapshot. */
export function workforceAgentSummary(agent:Agent, access:UsageResource['usage_config']['access']={mode:'allowlist',group_members_enabled:false,allow:[],deny:[]}):DigitalEmployee {
 return projectWorkforceSnapshot({agent,usage:{version:0,usage_config:{access,trigger:{mention:true,keywords:[],regexes:[]},history:{enabled:false,limit:20,channels:[]}}},rules:{version:0,rules:[]},bindings:{},packages:[],triggers:[],targets:[]}).employee;
}
export function acceptWorkforceAgentWrite(w:string,agent:Agent):DigitalEmployee {
 const current=cache.get(key(w,agent.id));
 invalidateWorkforcePlatformSnapshot(w,agent.id);
 if(!current)return workforceAgentSummary(agent);
 const next=projectWorkforceSnapshot({...current,agent});
 next.employee.channelReadErrors = current.employee.channelReadErrors;
 cache.set(key(w,agent.id),next);
 return structuredClone(next.employee);
}
export function addObservedWorkforceBindings(w:string,a:string,bindings:FeishuBinding[]) {
 const current=cache.get(key(w,a));
 return bindings.flatMap(binding=>{
  const provider=binding.provider as ChannelProvider;
  if(!current || current.bindings[provider])return [];
  current.bindings[provider]=binding;
  const channel=bindingForWorkforce(binding,current.usage);
  current.employee.channels.push(channel);
  if(current.employee.resourceVersions)current.employee.resourceVersions.channels[provider]=binding.config_revision;
  return [{channel,revision:binding.config_revision}];
 });
}
export async function getWorkforceEmployeePage(workspace: string, page = 1, signal?: AbortSignal, filter?: AgentDirectoryFilter) {
  const result = await agentsApi.list(workspace, page, signal, filter);
  const items = result.items.map(agent => {
    const employee = workforceAgentSummary(agent);
    const summary = agent.card_resources;
    if (summary) {
      employee.cardResources = {channels: summary.channels, packageCount: summary.package_count, triggerCount: summary.trigger_count};
    }
    return employee;
  });
  return {...result, items};
}
export async function listWorkforcePlatformEmployees(w:string,signal?:AbortSignal):Promise<DigitalEmployee[]> {
 return (await getWorkforceEmployeePage(w,1,signal)).items;
}
