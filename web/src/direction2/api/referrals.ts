import { platformRequest, post } from './platform';

export const referralSettingsChanged = 'kiko:referral-settings-changed';

type ReferralLink = { enabled: boolean; code?: string };
type ReferralSettings = { enabled: boolean; version: number };

const codePattern = /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/;

export function referralCodeFromPath(path: string): string {
  if (!path.startsWith('/r/')) return '';
  const code = path.slice(3);
  return codePattern.test(code) ? code : '';
}

export const referralsApi = {
  link: (init?: RequestInit) => platformRequest<ReferralLink>('/referrals/me', init),
  create: () => platformRequest<ReferralLink>('/referrals/me', post({})),
  settings: () => platformRequest<ReferralSettings>('/platform/referral-settings'),
  saveSettings: (settings: ReferralSettings) => platformRequest<ReferralSettings>('/platform/referral-settings', {
    method: 'PATCH',
    body: JSON.stringify({ enabled: settings.enabled, expected_version: settings.version })
  })
};
