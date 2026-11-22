import { platformRequest, post, type WorkspaceWire } from './platform';
import type { WorkspaceMember } from '../types';

interface Page<T> { items: T[]; total: number }
type MemberWire = Omit<WorkspaceMember, 'joinedAt'> & { joined_at: string; version: number };
export interface WorkspaceInvitation {
  id: string;
  kind: 'link' | 'email';
  email?: string;
  shared?: boolean;
  role: string;
  expires_at: string;
  status: string;
  delivery_status: string;
  link?: string;
}
interface InvitationInput {
  role: string;
  kind: 'link' | 'email';
  emails?: string[];
  expires_in_days: number;
}
const base = (id: string) => '/workspaces/' + encodeURIComponent(id);
const paging = (page: number) => `?page=${page}&page_size=20`;
function request<T>(path: string, init: RequestInit & {signal: AbortSignal}) {
  return platformRequest<T>(path, {...init, signal: AbortSignal.any([init.signal, AbortSignal.timeout(20000)])});
}
const patch = (body: object, signal: AbortSignal) => ({method: 'PATCH', body: JSON.stringify(body), signal});
export const workspaceSettings = {
  get: (id: string, signal: AbortSignal) => request<WorkspaceWire>(base(id), {signal}),
  save: (id: string, name: string, description: string, version: number, signal: AbortSignal) =>
    request<WorkspaceWire>(base(id), patch({name, description, version}, signal)),
  members: (id: string, page: number, signal: AbortSignal) =>
    request<Page<MemberWire>>(base(id) + '/members' + paging(page), {signal}),
  member: (id: string, member: string, changes: {role?: string; status?: string; version: number}, signal: AbortSignal) =>
    request<MemberWire>(base(id) + '/members/' + encodeURIComponent(member), patch(changes, signal)),
  invitations: (id: string, page: number, signal: AbortSignal) =>
    request<Page<WorkspaceInvitation>>(base(id) + '/invitations' + paging(page), {signal}),
  invite: (id: string, body: InvitationInput, signal: AbortSignal) =>
    request<WorkspaceInvitation>(base(id) + '/invitations', {...post(body), signal}),
  revoke: (id: string, invitation: string, signal: AbortSignal) =>
    request(base(id) + '/invitations/' + encodeURIComponent(invitation) + '/revoke', {...post({}), signal}),
  retry: (id: string, invitation: string, acknowledge: boolean, signal: AbortSignal) =>
    request(base(id) + '/invitations/' + encodeURIComponent(invitation) + '/retry-delivery', {...post({acknowledge_possible_duplicate: acknowledge}), signal})
};
export const memberFromWire = (m: MemberWire) => ({...m, joinedAt: new Date(m.joined_at).toLocaleDateString()});
