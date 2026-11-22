import { usageApi, type UsageResource, type UsageChannel } from './agent-usage';
import { workforceTriggerRulesApi } from './workforce-trigger-rules';

export interface GroupPolicyDraft {historyEnabled:boolean;historyLimit:number;historyChannels:UsageChannel[]}
export function groupPolicyDraft(resource:UsageResource):GroupPolicyDraft {
 const value=resource.usage_config;
 return {historyEnabled:value.history.enabled,historyLimit:value.history.limit,historyChannels:[...value.history.channels]};
}
/** Channel edits preserve other channels' effective enabled state, including a disabled global switch. */
export function replaceChannelHistory(resource:UsageResource,channel:UsageChannel,enabled:boolean,limit:number):UsageResource {
 const next:UsageResource=JSON.parse(JSON.stringify(resource));
 const old=resource.usage_config.history;
 const channels=old.enabled?[...old.channels]:[];
 next.usage_config.history={enabled:enabled||channels.some(v=>v!==channel),limit,channels:enabled?[...new Set([...channels,channel])]:channels.filter(v=>v!==channel)};
 return next;
}
/** Preserve access, named-rule projections, invisible channel state and CAS. */
export function replaceGroupPolicy(resource:UsageResource,draft:GroupPolicyDraft):UsageResource {
 const next:UsageResource=JSON.parse(JSON.stringify(resource));
 next.usage_config.history={enabled:draft.historyEnabled,limit:draft.historyLimit,channels:[...draft.historyChannels]};
 return next;
}
export const workforceGroupPoliciesApi={
 getUsage:usageApi.get,
 saveUsage:usageApi.save,
 getRules:workforceTriggerRulesApi.get,
 saveRules:workforceTriggerRulesApi.save,
 testRules:workforceTriggerRulesApi.test
};
