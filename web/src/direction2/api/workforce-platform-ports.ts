import {workforceGroupPoliciesApi} from './workforce-group-policies';
import type {WorkforcePorts} from './workforce-ports';
import {createWorkforcePlatformActions} from './workforce-platform-actions';
import {createWorkforceUsageActions} from './workforce-usage';

/** Never use a prototype fallback for an unsupported real operation. Incomplete
 * sections fail explicitly until their business contract is mapped. */
function strictGroup<T extends object>(implemented:Partial<T>,label:string):T {
 return new Proxy(implemented,{get(target,key){
  if(key in target)return Reflect.get(target,key);
  if(typeof key!=='string')return undefined;
  return ()=>{throw new Error(`${label}的「${key}」尚未接入真实接口，未执行任何保存`);};
 }}) as T;
}
export function createWorkforcePlatformPorts():WorkforcePorts {
 const actions={...createWorkforcePlatformActions(),...createWorkforceUsageActions()};
 return {
  groupPoliciesApi:workforceGroupPoliciesApi,
  workforceApi:strictGroup<WorkforcePorts['workforceApi']>(actions,'AI 员工配置'),
  resourcesApi:strictGroup<WorkforcePorts['resourcesApi']>({},'员工资源'),
  tasksApi:strictGroup<WorkforcePorts['tasksApi']>({},'员工任务')
 };
}
