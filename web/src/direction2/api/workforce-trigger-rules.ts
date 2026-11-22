import {platformRequest} from './platform';
import type {FeishuTriggerRule} from '../types';
export interface WorkforceNamedRule extends FeishuTriggerRule {channels:('feishu'|'web')[]}
export interface WorkforceTriggerRules {version:number;rules:WorkforceNamedRule[]}
const base=(w:string,a:string)=>`/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/trigger-rules`;
export const workforceTriggerRulesApi={
 get:(w:string,a:string,signal?:AbortSignal)=>platformRequest<WorkforceTriggerRules>(base(w,a),signal?{signal}:{}),
 save:(w:string,a:string,resource:WorkforceTriggerRules,signal?:AbortSignal)=>platformRequest<WorkforceTriggerRules>(base(w,a),{method:'PATCH',body:JSON.stringify(resource),...(signal?{signal}:{})}),
 test:(w:string,a:string,text:string,rules:WorkforceNamedRule[],channel:'feishu'|'web'='feishu')=>platformRequest<{matched_count:number;matched:{rule_id:string;rule_name:string;type:string;matched_text:string}[]}>(`${base(w,a)}/test`,{method:'POST',body:JSON.stringify({channel,text,rules})})
};

/** Edit Feishu-facing rows without dropping Web-only configuration or silently
 * changing a shared rule's Web behavior. Shared rows stay shared when edited. */
export function replaceFeishuTriggerRules(current:WorkforceTriggerRules, edited:FeishuTriggerRule[]):WorkforceTriggerRules {
 const existing=new Map(current.rules.map(rule=>[rule.id,rule]));
 const editedIds=new Set(edited.map(rule=>rule.id));
 const unaffected=current.rules.filter(rule=>!rule.channels.includes('feishu'));
 const detached=current.rules.filter(rule=>rule.channels.includes('feishu')&&!editedIds.has(rule.id)&&rule.channels.includes('web')).map(rule=>({...rule,channels:['web'] as ('feishu'|'web')[]}));
 return {version:current.version,rules:[...unaffected,...detached,...edited.map(rule=>({
  ...rule,id:existing.has(rule.id)?rule.id:'',channels:existing.get(rule.id)?.channels || ['feishu'] as ('feishu'|'web')[]
 }))]};
}
