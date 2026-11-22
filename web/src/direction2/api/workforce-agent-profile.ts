import { agentsApi, type Agent } from './agents';
import type { DigitalEmployee } from '../types';

/** Only fields with identical semantics in the original UI and platform API.
 * Status/statistics, model billing, and access policy are deliberately excluded. */
export type WorkforceAgentProfile = Pick<DigitalEmployee, 'id' | 'version' | 'name' | 'role' | 'avatar' | 'prompt'>;
export function workforceAgentProfile(agent: Agent): WorkforceAgentProfile {
  return { id: agent.id, version: agent.version, name: agent.name, role: agent.role, avatar: agent.avatar, prompt: agent.instruction };
}
export function createWorkforceAgentProfileTransport(api: Pick<typeof agentsApi, 'update'> = agentsApi) {
  return {
    async save(workspaceId: string, current: Agent, patch: Partial<Omit<WorkforceAgentProfile, 'id' | 'version'>>): Promise<Agent> {
      // Explicit allowlist keeps unrelated pending UI drafts out of this save.
      return api.update(workspaceId, current, {
        ...(patch.name !== undefined ? { name: patch.name } : {}),
        ...(patch.role !== undefined ? { role: patch.role } : {}),
        ...(patch.avatar !== undefined ? { avatar: patch.avatar } : {}),
        ...(patch.prompt !== undefined ? { instruction: patch.prompt } : {})
      });
    }
  };
}
