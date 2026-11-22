import type {workforceGroupPoliciesApi} from './workforce-group-policies';
import { inject, type InjectionKey } from 'vue';
import { resourcesApi, tasksApi, workforceApi } from './index';
import {realPlatform} from './platform';
import {createWorkforcePlatformPorts} from './workforce-platform-ports';
import type {channelsApi} from './channels';
import type {DigitalEmployee, PlatformType} from '../types';

export interface WorkforceChannelStatusOptions {
  channel?: PlatformType | undefined;
  channels?: PlatformType[] | undefined;
  signal?: AbortSignal | undefined;
}

/** Transport seams for the original Workforce components. Views and interactions
 * stay in those components; adapters must provide the complete operation group.
 * Never merge a partial real adapter with Mock methods. */
export interface WorkforcePorts {
  groupPoliciesApi?: typeof workforceGroupPoliciesApi;
  workforceApi: Omit<typeof workforceApi, 'getEmployeeChannelStatuses'> & {
    getEmployeeChannelStatuses: (id: string, workspace?: string, options?: WorkforceChannelStatusOptions) =>
      ReturnType<typeof workforceApi.getEmployeeChannelStatuses>;
    getEmployeeUsage?: (id: string, workspace: string, signal: AbortSignal) => Promise<DigitalEmployee>;
    cancelChannelQrRegistration?: (id: string) => Promise<unknown>;
    getChannelRebindImpact?: typeof channelsApi.rebindImpact;
  };
  resourcesApi: typeof resourcesApi;
  tasksApi: typeof tasksApi;
}
export const workforcePortsKey: InjectionKey<WorkforcePorts> = Symbol('workforce-ports');
const prototypePorts: WorkforcePorts = { workforceApi, resourcesApi, tasksApi };
let platformPorts: WorkforcePorts | undefined;
export function useWorkforcePorts(): WorkforcePorts {
  const override=inject(workforcePortsKey,undefined);
  if(override)return override;
  if(realPlatform){platformPorts??=createWorkforcePlatformPorts();return platformPorts;}
  return prototypePorts;
}
