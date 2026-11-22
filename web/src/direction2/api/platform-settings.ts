import { platformRequest, post } from './platform';

export interface MailSettingsFields {
  enabled: boolean;
  registration_verification_required: boolean;
  address: string;
  host: string;
  username: string;
  from: string;
  implicit_tls: boolean;
}
export type MailSettings = MailSettingsFields & {
  version: number;
  source: 'environment' | 'database';
  password_configured: boolean;
};
export type MailSettingsWrite = MailSettingsFields & { version: number; password?: string };

export interface GithubOAuthApp {
  provider: 'github';
  purpose: 'plugin';
  version: number;
  enabled: boolean;
  client_id: string;
  has_client_secret: boolean;
  source: 'unset' | 'legacy_env' | 'database';
  redirect_uri: string;
}
export interface GithubOAuthWrite {
  expected_version: number;
  enabled: boolean;
  client_id?: string;
  client_secret?: string;
  clear_client_secret?: boolean;
}

const mailPath = '/platform/mail-settings';
const githubPath = '/platform/oauth-apps/github';
const bounded = (signal: AbortSignal) => AbortSignal.any([signal, AbortSignal.timeout(20_000)]);
const update = <T>(path: string, data: unknown, signal: AbortSignal) =>
  platformRequest<T>(path, { method: 'PATCH', body: JSON.stringify(data), signal: bounded(signal) });

export const platformSettings = {
  mail: (signal: AbortSignal) => platformRequest<MailSettings>(mailPath, { signal: bounded(signal) }),
  saveMail: (data: MailSettingsWrite, signal: AbortSignal) => update<MailSettings>(mailPath, data, signal),
  testMail: (data: MailSettingsWrite, signal: AbortSignal) =>
    platformRequest<{ok: true}>(mailPath + '/test', { ...post(data), signal: bounded(signal) }),
  github: (signal: AbortSignal) => platformRequest<GithubOAuthApp>(githubPath, { signal: bounded(signal) }),
  saveGithub: (data: GithubOAuthWrite, signal: AbortSignal) => update<GithubOAuthApp>(githubPath, data, signal),
};
