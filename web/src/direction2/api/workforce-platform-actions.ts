import {D2ApiError,activeWorkspaceId} from './client';
import {agentsApi} from './agents';
import {modelSelection} from './models';
import {acceptWorkforceAgentWrite,acceptWorkforceUsageRead,addObservedWorkforceBindings,getWorkforcePlatformEmployee,getWorkforcePlatformSnapshot,invalidateWorkforcePlatformSnapshot,listWorkforcePlatformEmployees,readWorkforceChannel,workforceAgentSummary,workforceChannelProviders} from './workforce-platform-data';
import type {WorkforcePlatformSnapshot} from './workforce-platform-data';
import {createWorkforceChannelActions} from './workforce-channel-actions';
import {createWorkforceRegistrationTransport} from './workforce-channel-registration';
import {replaceFeishuTriggerRules,workforceTriggerRulesApi} from './workforce-trigger-rules';
import {usageApi} from './agent-usage';
import {bindingForWorkforce} from './workforce-channel-binding';
import type {DigitalEmployee,EmployeeChannelBinding,FeishuTriggerRule,PlatformType} from '../types';
import type {WorkforceChannelStatusOptions, WorkforcePorts} from './workforce-ports';
type ChannelStatusResult = Awaited<ReturnType<WorkforcePorts['workforceApi']['getEmployeeChannelStatuses']>>;

export function createWorkforcePlatformActions(workspace=()=>activeWorkspaceId.value){
 const snapshot=(id:string)=>{const item=getWorkforcePlatformSnapshot(workspace(),id);if(!item)throw new Error('请重新加载员工后再保存');return item;};
 const fresh=async(w:string,id:string,policy?:Pick<WorkforcePlatformSnapshot,'usage'|'rules'>)=>{invalidateWorkforcePlatformSnapshot(w,id);return getWorkforcePlatformEmployee(w,id,undefined,policy);};
 const version=(id:string,expected?:number)=>{const item=snapshot(id);if(expected!==undefined&&item.agent.version!==expected)throw new D2ApiError(409,'version_conflict','员工已更新，请重新加载');return item;};
 const channel=createWorkforceChannelActions({workspace,currentBinding:(w,a,p)=>getWorkforcePlatformSnapshot(w,a)?.bindings[p],reloadEmployee:fresh});
 const registration=createWorkforceRegistrationTransport({workspace,binding:async(w,a,p)=>(await fresh(w,a)).channels.find(x=>x.platform===p)});
 async function updateBasics(id:string,patch:Record<string,unknown>,expected?:number){
  const current=version(id,expected);const config:Parameters<typeof agentsApi.update>[2]={};
  for(const field of ['name','role','avatar'] as const)if(typeof patch[field]==='string')config[field]=patch[field];
  if(typeof patch.prompt==='string')config.instruction=patch.prompt;
  if(typeof patch.modelId==='string' && patch.modelId!==current.employee.modelId)Object.assign(config,modelSelection(patch.modelId));
  if(patch.confirm_platform_billing===true)config.confirm_platform_billing=true;
  if(patch.executionEngine!==undefined&&patch.executionEngine!=='pi')throw new Error('当前平台仅支持 Pi 执行引擎');
  if(patch.thinkingLevel!==undefined&&patch.thinkingLevel!==null)throw new Error('思考档位暂无可配置数据');
  const w=workspace();const saved=await agentsApi.update(w,current.agent,config);return acceptWorkforceAgentWrite(w,saved);
 }
 return {
  ...channel,...registration,
  listEmployees:()=>listWorkforcePlatformEmployees(workspace()),
  getEmployee:(id:string)=>getWorkforcePlatformEmployee(workspace(),id),
  async getEmployeeUsage(id: string, w: string, signal: AbortSignal) {
    const usage = await usageApi.get(w, id, AbortSignal.any([signal, AbortSignal.timeout(20000)]));
    signal.throwIfAborted();
    return acceptWorkforceUsageRead(w, id, usage);
  },
  async getEmployeeChannelStatuses(id: string, w = workspace(), options?: WorkforceChannelStatusOptions): Promise<ChannelStatusResult> {
    const current = getWorkforcePlatformSnapshot(w, id);
    if (!current) throw new Error('请重新加载员工配置');
    const providers = options?.channel ? [options.channel] : (options?.channels ?? workforceChannelProviders);
    const reads = await Promise.all(providers.map(async provider => [
      provider, await readWorkforceChannel(w, id, provider, options?.signal),
    ] as const));
    options?.signal?.throwIfAborted();
    const discovered: typeof current.bindings = {};
    const channelReadErrors: Partial<Record<PlatformType, string | null>> = {};
    const channels = reads.flatMap<ChannelStatusResult['channels'][number]>(([provider, {binding, error}]) => {
      const previous = current.bindings[provider];
      channelReadErrors[provider] = error ?? null;
      if (binding) {
        if (!previous) discovered[provider] = binding;
        return previous ? [{...bindingForWorkforce(binding, current.usage), observedConfigRevision: binding.config_revision}] : [];
      }
      if (!previous) return [];
      return [{
        platform: provider, connectionId: previous.id, status: previous.enabled ? 'pending_config' : 'disabled',
        statusText: previous.enabled ? '状态未知' : '已停用', connectionState: 'disconnected', boundGroups: [],
        ...(binding === null ? {bindingMissing: true as const} : {}),
        permissionReady: undefined, businessCapabilities: undefined, directoryStatus: undefined, identityConfirmed: undefined,
      }];
    });
    // Runtime refresh must not silently advance editable usage/rule CAS versions.
    const addedBindings = addObservedWorkforceBindings(w, id, Object.values(discovered));
    return {employeeId: id, status: current.employee.status, checkedAt: '', channels, addedBindings, channelReadErrors};
  },
  async createEmployee(payload:Record<string,unknown>){
    if(payload.executionEngine!==undefined&&payload.executionEngine!=='pi')throw new Error('当前平台仅支持 Pi 执行引擎');
    const w=workspace();const created=await agentsApi.create(w,{name:String(payload.name||''),role:String(payload.role||''),avatar:String(payload.avatar||''),instruction:String(payload.prompt||''),...modelSelection(String(payload.modelId||''))});
    return workforceAgentSummary(created,{mode:'all',group_members_enabled:true,allow:[],deny:[]});
  },
  updateEmployeeBasics:updateBasics,
  async updateEmployeeStatus(id:string,status:DigitalEmployee['status'],expected?:number){
    const current=version(id,expected);const enabled=status==='active';
    if(enabled&&current.agent.model_available===false)throw new Error('模型当前不可用，无法启用员工');
    if(!['active','disabled'].includes(status))throw new Error('该员工状态不支持直接设置');
    if(current.agent.enabled!==enabled)await agentsApi.setEnabled(workspace(),current.agent);
    return fresh(workspace(),id);
  },
  async saveFullEmployee(id:string,draft:DigitalEmployee,expected?:number){
    const current=version(id,expected);
    for(const key of ['channels','capabilityPackages','triggers','unifiedAccess','humanConfirmationRules','webChatAccess','handoffPolicy'] as const){
      if(JSON.stringify(current.employee[key])!==JSON.stringify(draft[key]))throw new Error('请在对应配置卡片分别保存，不能跨配置覆盖版本');
    }
    return updateBasics(id,{name:draft.name,role:draft.role,avatar:draft.avatar,prompt:draft.prompt,modelId:draft.modelId,executionEngine:draft.executionEngine,thinkingLevel:draft.thinkingLevel},expected);
  },
  async saveEmployeeChannels(id:string,edited:EmployeeChannelBinding[],_expected?:number,_workspace?:string,resourceVersions?:DigitalEmployee['resourceVersions']){
    const w=workspace(),current=snapshot(id);const original=current.employee.channels;
    const changed=edited.filter(item=>JSON.stringify(item.capabilities)!==JSON.stringify(original.find(x=>x.platform===item.platform)?.capabilities));
    if(changed.length!==1)throw new Error('请一次保存一个渠道配置');
    const after=changed[0]!;const before=original.find(x=>x.platform===after.platform);
    if(!before)throw new Error('请先接入渠道');
    if(after.configurationChanged)throw new Error('渠道配置已变更，请重新加载后再保存');
    const rulesChanged=JSON.stringify(before.capabilities.feishuRules)!==JSON.stringify(after.capabilities.feishuRules);
    const historyChanged=before.capabilities.recentContextEnabled!==after.capabilities.recentContextEnabled||before.capabilities.recentContextWindow!==after.capabilities.recentContextWindow;
    if(rulesChanged&&historyChanged)throw new Error('请分别保存触发规则和历史设置');
    if(rulesChanged){
      if(resourceVersions?.triggerRules===undefined)throw new Error('规则版本缺失，请重新加载');
      const rules = current.rules ?? await workforceTriggerRulesApi.get(w, id);
      const saved = await workforceTriggerRulesApi.save(w,id,replaceFeishuTriggerRules({...rules,version:resourceVersions.triggerRules},after.capabilities.feishuRules||[]));
      return fresh(w,id,{rules:saved});
    }
    if(historyChanged){
      if(resourceVersions?.usageHistory===undefined)throw new Error('历史配置版本缺失，请重新加载');
      const next=structuredClone(current.usage ?? await usageApi.get(w,id,AbortSignal.timeout(20000)));next.version=resourceVersions.usageHistory;next.usage_config.history.limit=after.capabilities.recentContextWindow??20;
      const channels=new Set(next.usage_config.history.channels);
      if(after.capabilities.recentContextEnabled)channels.add(after.platform);else channels.delete(after.platform);
      next.usage_config.history.channels=[...channels];next.usage_config.history.enabled=channels.size>0;
      const saved = await usageApi.save(w,id,next,AbortSignal.timeout(20000));
      return fresh(w,id,{usage:saved});
    }
    throw new Error('此配置不能通过渠道保存，请在统一权限配置中修改');
  },
  async testFeishuRules(text:string,rules:FeishuTriggerRule[],_workspace?:string,id?:string){
    if(!id)throw new Error('请先选择需要测试规则的员工');
    const w = workspace();
    const current = getWorkforcePlatformSnapshot(w, id)?.rules ?? await workforceTriggerRulesApi.get(w, id);
    const result=await workforceTriggerRulesApi.test(w,id,text,replaceFeishuTriggerRules(current,rules).rules);
    return {matchedCount:result.matched_count,matched:result.matched.map(x=>({ruleId:x.rule_id,ruleName:x.rule_name,type:x.type,matchedText:x.matched_text}))};
  }
 };
}
