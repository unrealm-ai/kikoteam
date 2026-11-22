<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue';
import {
  NTabs, NTabPane, NForm, NFormItem, NInput, NButton, NAlert, NModal, NDivider,
  useMessage, type FormInst, type FormRules
} from 'naive-ui';
import { authApi } from '../api';
import { realPlatform, validPlatformPassword, platformAuth, type AuthConfig } from '../api/platform';
import { setAuthToken } from '../api/client';
import type { AuthSessionResponse } from '../types';
import { productDescriptor } from '../product-brand';

const props = defineProps<{
  initialTab?: 'login' | 'register' | undefined;
  initialMethod?: 'password' | 'forgot';
  initialEmail?: string;
  preferCodeRegistration?: boolean;
}>();

const emit = defineEmits<{
  (e: 'authenticated', session: AuthSessionResponse): void;
  (e: 'session-invalidated'): void;
}>();

const message = useMessage();
const isDevMode = import.meta.env.DEV && !realPlatform;

const activeTab = ref<'login' | 'register'>(props.initialTab === 'register' ? 'register' : 'login');
const loginMethod = ref<'password' | 'code' | 'forgot'>(props.initialMethod || 'password');

const authConfig = ref<AuthConfig | null>(null);
const configLoading = ref(realPlatform);
const registrationNeedsCode = computed(() => !realPlatform || (authConfig.value?.registration_verification_required === true || (props.preferCodeRegistration === true && authConfig.value?.email_code_enabled === true)));
const registrationAvailable = computed(() => !realPlatform || (registrationNeedsCode.value ? authConfig.value?.email_code_enabled === true : authConfig.value?.password_registration_enabled === true));
const codeEnabled = computed(() => !realPlatform || authConfig.value?.email_code_enabled === true);
async function loadAuthConfig() {
  if (!realPlatform) return;
  configLoading.value = true;
  try {
    authConfig.value = await platformAuth.getConfig();
    if (loginMethod.value === 'forgot' && !authConfig.value.email_code_enabled) {
      loginMethod.value = 'password'; authError.value = '平台暂未开放邮件验证，请联系管理员配置邮件服务后再验证邮箱。';
    }
  }
  catch (e) { authError.value = e instanceof Error ? e.message : '无法读取登录配置'; }
  finally { configLoading.value = false; }
}
const authError = ref('');
const authSuccess = ref('');
const isSubmitting = ref(false);
const isStartingGoogleOAuth = ref(false);
const isProcessingOAuthCallback = ref(false);

// Login forms
const loginPasswordFormRef = ref<FormInst | null>(null);
const loginPasswordModel = reactive({
  email: props.initialEmail || '',
  password: ''
});

const loginCodeFormRef = ref<FormInst | null>(null);
const loginCodeModel = reactive({
  email: '',
  code: ''
});

// Forgot / Reset Password form
const forgotFormRef = ref<FormInst | null>(null);
const forgotModel = reactive({
  email: props.initialEmail || '',
  code: '',
  newPassword: '',
  confirmPassword: ''
});

// Register form
const registerFormRef = ref<FormInst | null>(null);
const registerModel = reactive({
  name: '',
  email: '',
  code: '',
  password: ''
});

// Verification code countdown, resend tracking & local debug hint
const sendingPurpose = ref<'register' | 'login' | 'reset_password' | null>(null);
const registerCountdown = ref(0);
const loginCountdown = ref(0);
const forgotCountdown = ref(0);
const hasSentRegisterCode = ref(false);
const hasSentLoginCode = ref(false);
const hasSentForgotCode = ref(false);
const registerDebugCode = ref('');
const loginDebugCode = ref('');
const forgotDebugCode = ref('');
let countdownTimer: ReturnType<typeof setInterval> | null = null;

// Google OAuth interactive account picker (when local mock server returns isMock: true)
const showMockGoogleModal = ref(false);
const isConfirmingMockGoogle = ref(false);
const pendingGoogleState = ref('');
const pendingGoogleRedirectUri = ref('');
const mockGoogleFormRef = ref<FormInst | null>(null);
const mockGoogleModel = reactive({
  email: '',
  name: ''
});

const EMAIL_REGEX = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

function isValidEmail(val: string): boolean {
  return val.trim().length <= 254 && EMAIL_REGEX.test(val.trim());
}

function startCountdown(purpose: 'register' | 'login' | 'reset_password', seconds = 60) {
  if (purpose === 'register') {
    registerCountdown.value = seconds;
    hasSentRegisterCode.value = true;
  } else if (purpose === 'login') {
    loginCountdown.value = seconds;
    hasSentLoginCode.value = true;
  } else {
    forgotCountdown.value = seconds;
    hasSentForgotCode.value = true;
  }
  if (!countdownTimer) {
    countdownTimer = setInterval(() => {
      if (registerCountdown.value > 0) registerCountdown.value -= 1;
      if (loginCountdown.value > 0) loginCountdown.value -= 1;
      if (forgotCountdown.value > 0) forgotCountdown.value -= 1;
      if (
        registerCountdown.value <= 0 &&
        loginCountdown.value <= 0 &&
        forgotCountdown.value <= 0 &&
        countdownTimer
      ) {
        clearInterval(countdownTimer);
        countdownTimer = null;
      }
    }, 1000);
  }
}

function sendButtonLabel(countdown: number, hasSent: boolean): string {
  if (countdown > 0) return `${countdown}s 后重发`;
  return hasSent ? '重新发送验证码' : '发送验证码';
}

const loginPasswordRules: FormRules = {
  email: [
    { required: true, message: '请输入邮箱地址', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => isValidEmail(val || ''),
      message: '请输入格式正确的邮箱地址',
      trigger: 'blur'
    }
  ],
  password: [
    { required: true, message: '请输入登录密码', trigger: ['blur', 'input'] }
  ]
};

const loginCodeRules: FormRules = {
  email: [
    { required: true, message: '请输入邮箱地址', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => isValidEmail(val || ''),
      message: '请输入格式正确的邮箱地址',
      trigger: 'blur'
    }
  ],
  code: [
    { required: true, message: '请输入 6 位邮箱验证码', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => /^\d{6}$/.test((val || '').trim()),
      message: '验证码应为 6 位数字',
      trigger: 'blur'
    }
  ]
};

const forgotRules: FormRules = {
  email: [
    { required: true, message: '请输入注册邮箱地址', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => isValidEmail(val || ''),
      message: '请输入格式正确的邮箱地址',
      trigger: 'blur'
    }
  ],
  code: [
    { required: true, message: '请输入 6 位邮箱验证码', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => /^\d{6}$/.test((val || '').trim()),
      message: '请输入 6 位数字验证码',
      trigger: 'blur'
    }
  ],
  newPassword: [
    { required: true, message: '请设置新密码', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => realPlatform ? validPlatformPassword(val || '') : (val || '').length >= 8,
      message: realPlatform ? '密码需要 10–72 个 UTF-8 字节（中文通常占 3 字节）' : '新密码长度至少为 8 位',
      trigger: ['blur', 'input']
    }
  ],
  confirmPassword: [
    { required: true, message: '请再次输入新密码确认', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => val === forgotModel.newPassword,
      message: '两次输入的新密码不一致',
      trigger: ['blur', 'input']
    }
  ]
};

const registerRules = computed<FormRules>(() => ({
  name: [
    { required: true, message: '请输入姓名或昵称', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => {
        const len = (val || '').trim().length;
        return len >= 1 && len <= 40;
      },
      message: '姓名长度需在 1-40 个字符之间',
      trigger: 'blur'
    }
  ],
  email: [
    { required: true, message: '请输入邮箱地址', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => isValidEmail(val || ''),
      message: '请输入格式正确的邮箱地址',
      trigger: 'blur'
    }
  ],
  code: [
    { required: true, message: '注册必须填写邮箱验证码', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => /^\d{6}$/.test((val || '').trim()),
      message: '请输入 6 位数字验证码',
      trigger: 'blur'
    }
  ],
  password: [
    { required: !realPlatform || !registrationNeedsCode.value, message: '请设置登录密码', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => realPlatform ? ((!val && registrationNeedsCode.value) || validPlatformPassword(val || '')) : (val || '').length >= 8,
      message: realPlatform ? '密码需要 10–72 个 UTF-8 字节（中文通常占 3 字节）' : '密码长度至少为 8 位',
      trigger: ['blur', 'input']
    }
  ]
}));

const mockGoogleRules: FormRules = {
  email: [
    { required: true, message: '请输入 Google 邮箱', trigger: ['blur', 'input'] },
    {
      validator: (_rule, val: string) => isValidEmail(val || ''),
      message: '请输入格式正确的邮箱地址',
      trigger: 'blur'
    }
  ],
  name: [
    { required: true, message: '请输入显示名称', trigger: ['blur', 'input'] }
  ]
};

function handleTabChange(val: string | number) {
  activeTab.value = val === 'register' ? 'register' : 'login';
  authError.value = '';
  authSuccess.value = '';
}

function switchLoginMethod(method: 'password' | 'code' | 'forgot') {
  if (method !== 'password' && !codeEnabled.value) return;
  if (method === 'forgot' && !forgotModel.email.trim() && loginPasswordModel.email.trim()) {
    forgotModel.email = loginPasswordModel.email.trim();
  } else if (method === 'code' && !loginCodeModel.email.trim() && loginPasswordModel.email.trim()) {
    loginCodeModel.email = loginPasswordModel.email.trim();
  }
  loginMethod.value = method;
  authError.value = '';
  if (method !== 'password') {
    authSuccess.value = '';
  }
}

function completeSession(session: AuthSessionResponse) {
  if (session.token) {
    setAuthToken(session.token);
  }
  emit('authenticated', session);
}

async function handleSendVerificationCode(purpose: 'register' | 'login' | 'reset_password') {
  const email = (
    purpose === 'register'
      ? registerModel.email
      : purpose === 'login'
        ? loginCodeModel.email
        : forgotModel.email
  ).trim();
  if (!email || !isValidEmail(email)) {
    authError.value = '请先输入格式正确的邮箱地址，再发送验证码';
    return;
  }
  const activeCd =
    purpose === 'register'
      ? registerCountdown.value
      : purpose === 'login'
        ? loginCountdown.value
        : forgotCountdown.value;
  if (sendingPurpose.value !== null || activeCd > 0) return;

  authError.value = '';
  authSuccess.value = '';
  sendingPurpose.value = purpose;
  try {
    const res = await authApi.sendEmailCode({ email, purpose });
    startCountdown(purpose, res.resendAfterSeconds || 60);
    if (purpose === 'register') {
      registerDebugCode.value = res.debugCode || '';
    } else if (purpose === 'login') {
      loginDebugCode.value = res.debugCode || '';
    } else {
      forgotDebugCode.value = res.debugCode || '';
    }
    message.success(`验证码已发送至 ${res.email}，${Math.floor((res.expiresInSeconds || 300) / 60)} 分钟内有效`);
  } catch (err) {
    authError.value = err instanceof Error ? err.message : '验证码发送失败，请稍后重试';
  } finally {
    sendingPurpose.value = null;
  }
}

async function handleEmailPasswordLogin() {
  authError.value = '';
  authSuccess.value = '';
  try {
    await loginPasswordFormRef.value?.validate();
  } catch {
    return;
  }
  isSubmitting.value = true;
  try {
    const session = await authApi.loginWithEmail({
      email: loginPasswordModel.email.trim(),
      password: loginPasswordModel.password
    });
    message.success(`欢迎回来，${session.user.name}`);
    completeSession(session);
  } catch (err) {
    authError.value = err instanceof Error ? err.message : '登录失败，请检查邮箱与密码';
  } finally {
    isSubmitting.value = false;
  }
}

async function handleEmailCodeLogin() {
  authError.value = '';
  authSuccess.value = '';
  try {
    await loginCodeFormRef.value?.validate();
  } catch {
    return;
  }
  isSubmitting.value = true;
  try {
    const session = await authApi.loginWithEmailCode({
      email: loginCodeModel.email.trim(),
      code: loginCodeModel.code.trim()
    });
    message.success(`欢迎回来，${session.user.name}`);
    completeSession(session);
  } catch (err) {
    authError.value = err instanceof Error ? err.message : '验证码登录失败';
  } finally {
    isSubmitting.value = false;
  }
}

async function handleResetPassword() {
  authError.value = '';
  authSuccess.value = '';
  try {
    await forgotFormRef.value?.validate();
  } catch {
    return;
  }
  isSubmitting.value = true;
  try {
    const res = await authApi.resetPassword({
      email: forgotModel.email.trim(),
      code: forgotModel.code.trim(),
      newPassword: forgotModel.newPassword
    });
    if (realPlatform) emit('session-invalidated');
    loginPasswordModel.email = res.email || forgotModel.email.trim();
    loginPasswordModel.password = '';
    forgotModel.code = '';
    forgotModel.newPassword = '';
    forgotModel.confirmPassword = '';
    forgotDebugCode.value = '';
    forgotCountdown.value = 0;
    hasSentForgotCode.value = false;
    loginMethod.value = 'password';
    authSuccess.value = '密码重置成功，请使用新密码登录';
    message.success('密码重置成功，请使用新密码登录');
  } catch (err) {
    authError.value = err instanceof Error ? err.message : '重置密码失败，请核对验证码或稍后重试';
  } finally {
    isSubmitting.value = false;
  }
}

async function handleRegister() {
  if (realPlatform && (!authConfig.value || configLoading.value || !registrationAvailable.value)) return;
  authError.value = '';
  authSuccess.value = '';
  try {
    await registerFormRef.value?.validate();
  } catch {
    return;
  }
  isSubmitting.value = true;
  try {
    const session = await authApi.registerWithEmail({
      name: registerModel.name.trim(),
      email: registerModel.email.trim(),
      code: registerModel.code.trim(),
      ...(realPlatform ? { method: registrationNeedsCode.value ? 'code' as const : 'password' as const } : {}),
      password: registerModel.password
    });
    message.success('注册成功，已登录');
    completeSession(session);
  } catch (err) {
    authError.value = err instanceof Error ? err.message : '注册失败，请检查填写信息';
  } finally {
    isSubmitting.value = false;
  }
}

function getOAuthRedirectUri(): string {
  return `${window.location.origin}${window.location.pathname}`;
}

async function handleGoogleOAuthLogin() {
  if (realPlatform || isStartingGoogleOAuth.value || isSubmitting.value) return;
  authError.value = '';
  authSuccess.value = '';
  isStartingGoogleOAuth.value = true;
  try {
    const redirectUri = getOAuthRedirectUri();
    const res = await authApi.getGoogleOAuthUrl(redirectUri);
    if (res.isMock) {
      pendingGoogleState.value = res.state;
      pendingGoogleRedirectUri.value = redirectUri;
      showMockGoogleModal.value = true;
      return;
    }
    window.location.assign(res.authorizeUrl);
  } catch (err) {
    authError.value = err instanceof Error ? err.message : '无法发起 Google OAuth 登录';
  } finally {
    isStartingGoogleOAuth.value = false;
  }
}

async function confirmMockGoogleConsent() {
  try {
    await mockGoogleFormRef.value?.validate();
  } catch {
    return;
  }
  isConfirmingMockGoogle.value = true;
  authError.value = '';
  try {
    const code = `gauth_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
    const session = await authApi.exchangeGoogleOAuthCallback({
      code,
      state: pendingGoogleState.value,
      redirectUri: pendingGoogleRedirectUri.value || getOAuthRedirectUri(),
      mockAccount: {
        email: mockGoogleModel.email.trim(),
        name: mockGoogleModel.name.trim()
      }
    });
    showMockGoogleModal.value = false;
    message.success(`已通过 Google 账号 ${session.user.email} 登录`);
    completeSession(session);
  } catch (err) {
    authError.value = err instanceof Error ? err.message : 'Google 授权登录失败';
  } finally {
    isConfirmingMockGoogle.value = false;
  }
}

function cancelMockGoogleConsent() {
  showMockGoogleModal.value = false;
  pendingGoogleState.value = '';
}

function cleanOAuthParamsFromUrl() {
  if (typeof window === 'undefined') return;
  const url = new URL(window.location.href);
  const keysToDelete = ['code', 'state', 'scope', 'authuser', 'prompt', 'error', 'error_description', 'mock_google_oauth'];
  let changed = false;
  for (const key of keysToDelete) {
    if (url.searchParams.has(key)) {
      url.searchParams.delete(key);
      changed = true;
    }
  }
  if (changed) {
    const nextUrl = `${url.pathname}${url.search}${url.hash}`;
    window.history.replaceState(null, '', nextUrl);
  }
}

async function checkAndConsumeOAuthCallbackOnMount() {
  if (realPlatform || typeof window === 'undefined') return;
  const params = new URLSearchParams(window.location.search);
  const oauthError = params.get('error');
  const code = params.get('code');
  const state = params.get('state');

  if (oauthError) {
    const desc = params.get('error_description');
    authError.value = desc ? `Google 授权未完成：${desc}` : '已取消或未完成 Google 账号授权';
    cleanOAuthParamsFromUrl();
    return;
  }

  if (code && state) {
    isProcessingOAuthCallback.value = true;
    authError.value = '';
    try {
      const session = await authApi.exchangeGoogleOAuthCallback({
        code,
        state,
        redirectUri: getOAuthRedirectUri()
      });
      cleanOAuthParamsFromUrl();
      message.success(`已通过 Google 账号 ${session.user.email} 登录`);
      completeSession(session);
    } catch (err) {
      cleanOAuthParamsFromUrl();
      authError.value = err instanceof Error ? err.message : 'Google OAuth 回调校验失败，请重新登录';
    } finally {
      isProcessingOAuthCallback.value = false;
    }
  }
}

onMounted(() => {
  void loadAuthConfig();
  checkAndConsumeOAuthCallbackOnMount();
});

onUnmounted(() => {
  if (countdownTimer) {
    clearInterval(countdownTimer);
    countdownTimer = null;
  }
});
</script>

<template>
  <div class="auth-page">
    <div class="auth-card">
      <div class="auth-brand-header">
        <div class="auth-brand-row">
          <img src="/brand/mark.svg" class="auth-brand-mark" alt="" aria-hidden="true" />
          <span class="auth-brand-name">KIKO</span>
          <span class="auth-brand-badge">{{ productDescriptor }}</span>
        </div>
        <h1 class="auth-title">
          {{ activeTab === 'register' ? '创建企业协同账号' : (loginMethod === 'forgot' ? '重置登录密码' : '登录到工作区') }}
        </h1>
        <p class="auth-subtitle">
          {{
            activeTab === 'register'
              ? '使用企业或个人邮箱创建协同账号'
              : loginMethod === 'forgot'
                ? '通过注册邮箱接收验证码并设置新的登录密码'
                : (realPlatform ? (codeEnabled ? '支持邮箱密码或邮箱验证码登录' : '使用邮箱和密码登录') : '支持邮箱密码、邮箱验证码或 Google 账号快速登录')
          }}
        </p>
      </div>

      <NAlert v-if="isProcessingOAuthCallback" type="info" :show-icon="true" class="auth-alert">
        正在完成 Google OAuth 身份校验，请稍候...
      </NAlert>

      <NAlert v-else-if="authError" type="error" :show-icon="true" closable class="auth-alert" @close="authError = ''">
        {{ authError }}
      </NAlert>

      <NAlert v-else-if="authSuccess" type="success" :show-icon="true" closable class="auth-alert" @close="authSuccess = ''">
        {{ authSuccess }}
      </NAlert>

      <NAlert v-if="realPlatform && !configLoading && !authConfig" type="warning"><NButton text @click="loadAuthConfig">重试加载登录配置</NButton></NAlert>
      <NAlert v-if="realPlatform && authConfig && activeTab === 'register' && !registrationAvailable" type="warning">当前注册方式暂不可用，请联系管理员配置邮件服务。</NAlert>
      <NTabs
        :value="activeTab"
        type="segment"
        animated
        class="auth-mode-tabs"
        @update:value="handleTabChange"
      >
        <NTabPane name="login" tab="登录">
          <NForm
            v-if="loginMethod === 'password'"
            ref="loginPasswordFormRef"
            :model="loginPasswordModel"
            :rules="loginPasswordRules"
            label-placement="top"
            :show-require-mark="false"
            class="auth-form"
            @submit.prevent="handleEmailPasswordLogin"
          >
            <NFormItem label="邮箱地址" path="email">
              <NInput
                v-model:value="loginPasswordModel.email"
                placeholder="name@company.com"
                size="large"
                :input-props="{ type: 'email', autocomplete: 'email' }"
              />
            </NFormItem>

            <NFormItem label="登录密码" path="password">
              <NInput
                v-model:value="loginPasswordModel.password"
                type="password"
                show-password-on="click"
                placeholder="请输入登录密码"
                size="large"
                :input-props="{ autocomplete: 'current-password' }"
              />
            </NFormItem>

            <div class="auth-method-row">
              <button v-if="codeEnabled" type="button" class="auth-link-btn" @click="switchLoginMethod('code')">
                使用邮箱验证码登录
              </button>
              <button v-if="codeEnabled" type="button" class="auth-link-btn" @click="switchLoginMethod('forgot')">
                忘记密码？
              </button>
            </div>

            <NButton
              type="primary"
              attr-type="submit"
              size="large"
              block
              :loading="isSubmitting"
              :disabled="isProcessingOAuthCallback || configLoading || (realPlatform && !authConfig)"
            >
              登录
            </NButton>
          </NForm>

          <NForm
            v-else-if="loginMethod === 'code'"
            ref="loginCodeFormRef"
            :model="loginCodeModel"
            :rules="loginCodeRules"
            label-placement="top"
            :show-require-mark="false"
            class="auth-form"
            @submit.prevent="handleEmailCodeLogin"
          >
            <NFormItem label="邮箱地址" path="email">
              <NInput
                v-model:value="loginCodeModel.email"
                placeholder="name@company.com"
                size="large"
                :input-props="{ type: 'email', autocomplete: 'email' }"
              />
            </NFormItem>

            <NFormItem label="邮箱验证码" path="code">
              <div class="verify-code-row">
                <NInput
                  v-model:value="loginCodeModel.code"
                  placeholder="6 位数字验证码"
                  size="large"
                  maxlength="6"
                  class="verify-code-input"
                  :input-props="{ inputmode: 'numeric', autocomplete: 'one-time-code' }"
                />
                <NButton
                  size="large"
                  class="send-code-btn"
                  :loading="sendingPurpose === 'login'"
                  :disabled="loginCountdown > 0 || sendingPurpose !== null || isSubmitting"
                  @click="handleSendVerificationCode('login')"
                >
                  {{ sendButtonLabel(loginCountdown, hasSentLoginCode) }}
                </NButton>
              </div>
            </NFormItem>

            <NAlert v-if="isDevMode && loginDebugCode" type="info" :show-icon="true" class="debug-code-alert">
              <div class="debug-code-row">
                <span>测试验证码：<strong class="tabular-num mono-code">{{ loginDebugCode }}</strong></span>
                <button type="button" class="auth-link-btn" @click="loginCodeModel.code = loginDebugCode">一键填入</button>
              </div>
            </NAlert>

            <div class="auth-method-row">
              <button type="button" class="auth-link-btn" @click="switchLoginMethod('password')">
                返回密码登录
              </button>
              <button v-if="codeEnabled" type="button" class="auth-link-btn" @click="switchLoginMethod('forgot')">
                忘记密码？
              </button>
            </div>

            <NButton
              type="primary"
              attr-type="submit"
              size="large"
              block
              :loading="isSubmitting"
              :disabled="isProcessingOAuthCallback || configLoading || (realPlatform && !authConfig)"
            >
              验证并登录
            </NButton>
          </NForm>

          <NForm
            v-else
            ref="forgotFormRef"
            :model="forgotModel"
            :rules="forgotRules"
            label-placement="top"
            :show-require-mark="true"
            class="auth-form"
            @submit.prevent="handleResetPassword"
          >
            <NFormItem label="注册邮箱" path="email">
              <NInput
                v-model:value="forgotModel.email"
                placeholder="name@company.com"
                size="large"
                :input-props="{ type: 'email', autocomplete: 'email' }"
              />
            </NFormItem>

            <NFormItem label="邮箱验证码" path="code">
              <div class="verify-code-row">
                <NInput
                  v-model:value="forgotModel.code"
                  placeholder="请输入 6 位验证码"
                  size="large"
                  maxlength="6"
                  class="verify-code-input"
                  :input-props="{ inputmode: 'numeric', autocomplete: 'one-time-code' }"
                />
                <NButton
                  size="large"
                  class="send-code-btn"
                  :loading="sendingPurpose === 'reset_password'"
                  :disabled="forgotCountdown > 0 || sendingPurpose !== null || isSubmitting"
                  @click="handleSendVerificationCode('reset_password')"
                >
                  {{ sendButtonLabel(forgotCountdown, hasSentForgotCode) }}
                </NButton>
              </div>
            </NFormItem>

            <NAlert v-if="isDevMode && forgotDebugCode" type="info" :show-icon="true" class="debug-code-alert">
              <div class="debug-code-row">
                <span>测试验证码：<strong class="tabular-num mono-code">{{ forgotDebugCode }}</strong></span>
                <button type="button" class="auth-link-btn" @click="forgotModel.code = forgotDebugCode">一键填入</button>
              </div>
            </NAlert>

            <NFormItem label="设置新密码" path="newPassword">
              <NInput
                v-model:value="forgotModel.newPassword"
                type="password"
                show-password-on="click"
                :placeholder="realPlatform ? '10–72 个 UTF-8 字节' : '至少 8 位字符'"
                size="large"
                :input-props="{ autocomplete: 'new-password' }"
              />
            </NFormItem>

            <NFormItem label="确认新密码" path="confirmPassword">
              <NInput
                v-model:value="forgotModel.confirmPassword"
                type="password"
                show-password-on="click"
                placeholder="请再次输入新密码"
                size="large"
                :input-props="{ autocomplete: 'new-password' }"
              />
            </NFormItem>

            <div class="auth-method-row">
              <button type="button" class="auth-link-btn" @click="switchLoginMethod('password')">
                返回密码登录
              </button>
              <button v-if="codeEnabled" type="button" class="auth-link-btn" @click="switchLoginMethod('code')">
                改用验证码直接登录
              </button>
            </div>

            <NButton
              type="primary"
              attr-type="submit"
              size="large"
              block
              :loading="isSubmitting"
              :disabled="isProcessingOAuthCallback || configLoading || (realPlatform && !authConfig)"
            >
              重置密码
            </NButton>
          </NForm>
        </NTabPane>

        <NTabPane name="register" tab="注册">
          <NForm
            ref="registerFormRef"
            :model="registerModel"
            :rules="registerRules"
            label-placement="top"
            :show-require-mark="true"
            class="auth-form"
            @submit.prevent="handleRegister"
          >
            <NFormItem label="姓名 / 昵称" path="name">
              <NInput
                v-model:value="registerModel.name"
                placeholder="例如：张明"
                size="large"
                :input-props="{ autocomplete: 'name' }"
              />
            </NFormItem>

            <NFormItem label="邮箱地址" path="email">
              <NInput
                v-model:value="registerModel.email"
                placeholder="name@company.com"
                size="large"
                :input-props="{ type: 'email', autocomplete: 'email' }"
              />
            </NFormItem>

            <NFormItem v-if="registrationNeedsCode" label="邮箱验证码" path="code">
              <div class="verify-code-row">
                <NInput
                  v-model:value="registerModel.code"
                  placeholder="请输入 6 位验证码"
                  size="large"
                  maxlength="6"
                  class="verify-code-input"
                  :input-props="{ inputmode: 'numeric', autocomplete: 'one-time-code' }"
                />
                <NButton
                  size="large"
                  class="send-code-btn"
                  :loading="sendingPurpose === 'register'"
                  :disabled="registerCountdown > 0 || sendingPurpose !== null || isSubmitting"
                  @click="handleSendVerificationCode('register')"
                >
                  {{ sendButtonLabel(registerCountdown, hasSentRegisterCode) }}
                </NButton>
              </div>
            </NFormItem>

            <NAlert v-if="isDevMode && registerDebugCode" type="info" :show-icon="true" class="debug-code-alert">
              <div class="debug-code-row">
                <span>测试验证码：<strong class="tabular-num mono-code">{{ registerDebugCode }}</strong></span>
                <button type="button" class="auth-link-btn" @click="registerModel.code = registerDebugCode">一键填入</button>
              </div>
            </NAlert>

            <NFormItem :label="realPlatform && registrationNeedsCode ? '设置密码（可选）' : '设置密码'" path="password">
              <NInput
                v-model:value="registerModel.password"
                type="password"
                show-password-on="click"
                :placeholder="realPlatform ? '10–72 个 UTF-8 字节' : '至少 8 位字符'"
                size="large"
                :input-props="{ autocomplete: 'new-password' }"
              />
            </NFormItem>

            <NButton
              type="primary"
              attr-type="submit"
              size="large"
              block
              :loading="isSubmitting"
              :disabled="isProcessingOAuthCallback || configLoading || (realPlatform && !authConfig) || !registrationAvailable"
            >
              创建账号并登录
            </NButton>
          </NForm>
        </NTabPane>
      </NTabs>

      <NDivider class="auth-divider">或通过第三方账号继续</NDivider>

      <div class="oauth-section">
        <NButton
          block
          size="large"
          class="google-oauth-btn"
          :loading="isStartingGoogleOAuth || isProcessingOAuthCallback"
          :disabled="realPlatform || isSubmitting"
          @click="handleGoogleOAuthLogin"
        >
          <template #icon>
            <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true">
              <path fill="#4285F4" d="M23.49 12.27c0-.79-.07-1.54-.2-2.27H12v4.3h6.46a5.52 5.52 0 0 1-2.4 3.62v3h3.88c2.27-2.09 3.55-5.17 3.55-8.65Z" />
              <path fill="#34A853" d="M12 24c3.24 0 5.95-1.07 7.94-2.91l-3.88-3c-1.07.72-2.44 1.15-4.06 1.15-3.12 0-5.77-2.11-6.72-4.95H1.28v3.11A12 12 0 0 0 12 24Z" />
              <path fill="#FBBC05" d="M5.28 14.29A7.2 7.2 0 0 1 4.9 12c0-.8.14-1.57.38-2.29V6.6H1.28A12 12 0 0 0 0 12c0 1.94.46 3.77 1.28 5.4l4-3.11Z" />
              <path fill="#EA4335" d="M12 4.75c1.76 0 3.34.61 4.58 1.8l3.44-3.44C17.95 1.19 15.24 0 12 0A12 12 0 0 0 1.28 6.6l4 3.11C6.23 6.86 8.88 4.75 12 4.75Z" />
            </svg>
          </template>
          {{ realPlatform ? 'Google 登录暂未开放' : '使用 Google 账号继续' }}
        </NButton>
      </div>

      <div class="auth-footer-note">
        通过邀请链接登录或注册后会自动加入工作区；指定邮箱邀请须使用对应邮箱。
      </div>
    </div>

    <!-- Google OAuth 本地授权选择弹窗 (仅当后端返回 isMock 时触发) -->
    <NModal
      v-model:show="showMockGoogleModal"
      preset="card"
      title="使用 Google 账号登录 KIKO"
      style="width: min(440px, 94vw);"
      :mask-closable="true"
    >
      <div class="google-picker-body">
        <p class="google-picker-desc">
          填写用于本地模拟登录的 Google 邮箱和姓名（不连接 Google）：
        </p>

        <NForm
          ref="mockGoogleFormRef"
          :model="mockGoogleModel"
          :rules="mockGoogleRules"
          label-placement="top"
          :show-require-mark="false"
        >
          <NFormItem label="Google 邮箱" path="email">
            <NInput v-model:value="mockGoogleModel.email" placeholder="user@gmail.com" />
          </NFormItem>
          <NFormItem label="Google 账户显示名称" path="name">
            <NInput v-model:value="mockGoogleModel.name" placeholder="Alex Chen" />
          </NFormItem>
        </NForm>
      </div>

      <template #footer>
        <div class="google-modal-footer">
          <NButton @click="cancelMockGoogleConsent">取消授权</NButton>
          <NButton type="primary" :loading="isConfirmingMockGoogle" @click="confirmMockGoogleConsent">
            授权并登录
          </NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.auth-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px 16px;
  background: var(--id-bg-canvas);
  color: var(--id-text-primary);
  font-family: var(--id-font-sans);
}

.auth-card {
  width: min(440px, 100%);
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.auth-brand-header {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.auth-brand-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.auth-brand-mark {
  width: 24px;
  height: 24px;
  display: block;
  object-fit: contain;
}

.auth-brand-name {
  font-size: 16px;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--id-text-primary);
}

.auth-brand-badge {
  font-size: 12px;
  font-weight: 500;
  color: var(--id-text-secondary);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-sm);
  padding: 2px 8px;
}

.auth-title {
  margin: 4px 0 0;
  font-size: 20px;
  line-height: 28px;
  font-weight: 600;
  color: var(--id-text-primary);
}

.auth-subtitle {
  margin: 0;
  font-size: 13px;
  line-height: 20px;
  color: var(--id-text-secondary);
}

.auth-alert {
  margin: 0;
}

.oauth-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.google-oauth-btn {
  font-weight: 500;
}

.auth-divider {
  margin: 0;
  font-size: 12px;
  color: var(--id-text-muted);
}

.auth-mode-tabs :deep(.n-tabs-pane-wrapper) {
  padding-top: 12px;
}

.auth-form {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.verify-code-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.verify-code-input {
  flex: 1;
  min-width: 0;
}

.verify-code-input :deep(input) {
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  letter-spacing: 0.08em;
}

.send-code-btn {
  flex-shrink: 0;
  min-width: 124px;
  font-variant-numeric: tabular-nums;
}

.debug-code-alert {
  margin-bottom: 12px;
}

.debug-code-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 13px;
}

.mono-code {
  font-family: var(--id-font-mono);
  color: var(--id-text-primary);
}

.auth-method-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  margin: 0 0 12px;
}

.auth-link-btn {
  appearance: none;
  border: none;
  background: transparent;
  padding: 0;
  font-family: inherit;
  font-size: 13px;
  font-weight: 500;
  color: var(--id-accent-primary);
  cursor: pointer;
}

.auth-link-btn:hover {
  color: var(--id-accent-primary-hover);
  text-decoration: underline;
}

.auth-link-btn:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
  border-radius: var(--id-radius-sm);
}

.auth-hint-row {
  margin-top: 8px;
  text-align: center;
}

.auth-hint-mono {
  font-size: 12px;
  color: var(--id-text-muted);
  font-family: var(--id-font-mono);
}

.auth-footer-note {
  font-size: 12px;
  line-height: 18px;
  color: var(--id-text-muted);
  border-top: 1px solid var(--id-border-hairline);
  padding-top: 12px;
}

.google-picker-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.google-picker-desc {
  margin: 0;
  font-size: 13px;
  line-height: 20px;
  color: var(--id-text-secondary);
}

.google-modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

@media (max-width: 640px) {
  .auth-page {
    padding: 16px 12px;
    align-items: flex-start;
  }

  .auth-card {
    padding: 20px 16px;
  }

  .verify-code-row {
    flex-wrap: wrap;
  }

  .send-code-btn {
    width: 100%;
  }
}
</style>
