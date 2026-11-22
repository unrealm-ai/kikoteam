import { platformRequest } from './platform';
export interface WecomContact { search_user_id: string; display_name: string; department_paths: string[]; verification_status: string; callback_user_id?: string | undefined; verification_id?: string | undefined }
export type WecomContactPermissionStatus = 'not_checked'|'available'|'denied'|'authentication_failed'|'unsupported'|'unavailable';
export interface WecomContactPermission { status: WecomContactPermissionStatus; binding_id: string; connection_generation: number; config_revision: number; checked_at?: string }
export interface WecomVerification { id: string; search_user_id: string; status: 'pending'|'verified'|'cancelled'|'expired'|'conflict'; callback_user_id?: string | undefined; code?: string | undefined; expires_at: string }
const path = (w: string, a: string) => `/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/channels/wecom/contacts`;
const bounded = (signal: AbortSignal) => AbortSignal.any([signal, AbortSignal.timeout(25000)]);
export const wecomContactsApi = {
 permission: (w: string,a: string,signal: AbortSignal) => platformRequest<WecomContactPermission>(`${path(w,a)}/permission`,{signal:bounded(signal)}),
 search: (w: string,a: string,q: string,signal: AbortSignal) => platformRequest<{items:WecomContact[];limited:boolean}>(`${path(w,a)}/search?${new URLSearchParams({q})}`,{signal:bounded(signal)}),
 start: (w: string,a: string,user: string,id: string,signal: AbortSignal) => platformRequest<WecomVerification>(`${path(w,a)}/verifications`,{method:'POST',body:JSON.stringify({search_user_id:user,request_id:id}),signal:bounded(signal)}),
 status: (w: string,a: string,id: string,signal: AbortSignal) => platformRequest<WecomVerification>(`${path(w,a)}/verifications/${encodeURIComponent(id)}`,{signal:bounded(signal)}),
 cancel: (w: string,a: string,id: string,signal: AbortSignal) => platformRequest<WecomVerification>(`${path(w,a)}/verifications/${encodeURIComponent(id)}/cancel`,{method:'POST',body:'{}',signal:bounded(signal)})
};
