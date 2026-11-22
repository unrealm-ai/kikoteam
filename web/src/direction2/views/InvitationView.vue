<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue';
import { NCard, NButton, NAlert, NSpin } from 'naive-ui';
import AuthView from './AuthView.vue';
import { platformRequest, post } from '../api/platform';
import { D2ApiError } from '../api/client';
import type { UserProfile, AuthSessionResponse } from '../types';
const props = defineProps<{ token: string; user: UserProfile | null }>();
const emit = defineEmits<{ (event: 'authenticated', value: AuthSessionResponse): void; (event: 'completed', workspaceId?: string): void; (event: 'signout'): void; (event: 'session-invalidated'): void }>();
interface Invitation { kind?: 'link' | 'email'; status: string; workspace_name?: string; email_masked?: string; contact_email?: string }
const invitation = ref<Invitation | null>(null);
const loading = ref(true);
const accepting = ref(false);
const showLogin = ref(false);
const error = ref('');
const verificationRequired = ref(false);
const verifying = ref(false);
const verificationEmail = ref('');
const requireFreshLogin = ref(false);
const authenticatedUser = ref<UserProfile | null>(null);
const activeUser = computed(() => requireFreshLogin.value ? null : authenticatedUser.value || props.user);
let autoAttempt = '';
function maybeAccept() {
  const key = `${props.token}:${activeUser.value?.id || ''}`;
  if (verifying.value || !activeUser.value || loading.value || !['valid','pending'].includes(invitation.value?.status || '') || autoAttempt === key) return;
  autoAttempt = key; void accept();
}
let generation = 0;
async function load() {
  const current = ++generation; loading.value = true; error.value = '';
  try { const result = await platformRequest<Invitation>(`/invitations/${encodeURIComponent(props.token)}`); if (current === generation) invitation.value = result; }
  catch (e) { if (current === generation) error.value = e instanceof Error ? e.message : '无法读取邀请'; }
  finally { if (current === generation) { loading.value = false; maybeAccept(); } }
}
async function accept() {
  if (accepting.value) return; accepting.value = true; error.value = '';
  try { const result = await platformRequest<{workspace_id:string}>(`/invitations/${encodeURIComponent(props.token)}/accept`, post({})); emit('completed', result.workspace_id); }
  catch (e) { verificationRequired.value = e instanceof D2ApiError && e.code === 'email_verification_required'; error.value = e instanceof D2ApiError && e.code === 'email_verification_required' ? '邮箱尚未验证，暂时无法接受定向邀请。请通过邮箱验证码重置密码完成验证；若平台未开启邮件功能，请联系管理员配置。' : e instanceof Error ? e.message : '加入失败'; }
  finally { accepting.value = false; }
}
function beginVerification() { verificationEmail.value = activeUser.value?.email || ''; verifying.value = true; }
function sessionInvalidated() { requireFreshLogin.value = true; authenticatedUser.value = null; emit('session-invalidated'); }
function authenticated(session: AuthSessionResponse) { requireFreshLogin.value = false; verifying.value = false; verificationRequired.value = false; autoAttempt = ''; showLogin.value = false; authenticatedUser.value = session.user; emit('authenticated', session); void load(); }
onMounted(load);
watch(() => props.token, () => { autoAttempt = ''; void load(); });
watch(() => props.user?.id, () => { if (!props.user) authenticatedUser.value = null; maybeAccept(); });
</script>
<template>
  <AuthView v-if="verifying || (showLogin && !activeUser)" :initial-method="verifying ? 'forgot' : 'password'" :initial-email="verificationEmail" :prefer-code-registration="invitation?.kind === 'email'" @session-invalidated="sessionInvalidated" @authenticated="authenticated" />
  <main v-else class="invitation-page">
    <NCard style="max-width: 500px" title="工作区邀请">
      <NSpin v-if="loading" />
      <template v-else-if="invitation">
        <template v-if="invitation.status === 'valid' || invitation.status === 'pending'">
          <h2>加入 {{ invitation.workspace_name || '团队工作区' }}</h2>
          <p v-if="invitation.kind === 'link'">注册或登录后将自动加入此工作区，作为成员参与协作。</p>
          <p v-if="invitation.email_masked">请使用 {{ invitation.email_masked }} 登录后确认加入。</p>
          <p v-else-if="invitation.kind === 'email'">请使用受邀邮箱注册或登录，验证邮箱后即可加入。</p>
          <p v-if="activeUser">当前账号：{{ activeUser.email }} <NButton text @click="emit('signout')">切换账号</NButton></p>
          <NButton v-if="!activeUser" type="primary" @click="showLogin = true">登录或注册后加入</NButton>
          <NButton v-else-if="verificationRequired" type="primary" @click="beginVerification">验证邮箱</NButton>
          <NButton v-else type="primary" :loading="accepting" @click="accept">{{ accepting ? '正在加入…' : '重试加入' }}</NButton>
        </template>
        <template v-else>
          <h2>{{ invitation.status === 'accepted' ? '邀请已接受' : '邀请链接已失效' }}</h2>
          <p v-if="invitation.status === 'accepted'">此邀请已使用。你可以返回平台查看工作区。</p>
          <p v-else-if="invitation.contact_email">请联系 {{ invitation.contact_email }} 重新邀请。</p>
          <p v-else>请联系邀请人获取新的邀请链接。</p>
          <NButton @click="emit('completed')">返回平台</NButton>
        </template>
      </template>
      <p v-if="activeUser && (error || !['valid','pending'].includes(invitation?.status || ''))">账号已登录。未加入受邀工作区不影响使用自己的工作区。<NButton text @click="emit('completed')">进入自己的工作区</NButton></p>
      <p class="invite-privacy">邀请信息仅在本页临时保留。刷新或关闭后如需继续，请重新打开原邀请链接。</p>
      <NAlert v-if="error" type="error" style="margin-top: 16px">{{ error }}<br><NButton text @click="load">重新加载邀请</NButton></NAlert>
    </NCard>
  </main>
</template>
<style scoped>
.invitation-page { min-height: 100vh; display:flex; align-items:center; justify-content:center; padding:24px; background:#f4f7fb; }
</style>
