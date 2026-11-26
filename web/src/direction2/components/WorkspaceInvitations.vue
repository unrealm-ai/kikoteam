<script setup lang="ts">
import { copyText } from '../utils/clipboard';
import {computed, onMounted, onUnmounted, ref, watch} from 'vue';
import {NAlert, NButton, NEmpty, NForm, NFormItem, NInput, NInputNumber, NModal, NPagination, NSelect, useMessage} from 'naive-ui';
import {platformAuth} from '../api/platform';
import {workspaceSettings, type WorkspaceInvitation} from '../api/workspace-settings';
const props = defineProps<{workspace: string; owner: boolean}>();
const open = defineModel<boolean>('open', {required:true});
const toast = useMessage();
const controller = new AbortController();
const signal = controller.signal;
const inviteBoxRef = ref<HTMLElement | null>(null);
const copiedLink = ref(false);
let copiedTimer: ReturnType<typeof setTimeout> | undefined;
onUnmounted(() => {
  clearTimeout(copiedTimer);
  controller.abort();
});
const mailEnabled = ref(false);
onMounted(async () => {
  try { mailEnabled.value = (await platformAuth.getConfig()).email_code_enabled === true; }
  catch { mailEnabled.value = false; }
});
const addresses = ref('');
const emails = computed(() => [...new Set(addresses.value.trim().toLowerCase().split(/[\s,;，；]+/).filter(Boolean))]);
const targeted = computed(() => mailEnabled.value && addresses.value.trim() !== '');
const validEmails = computed(() => (!targeted.value || emails.value.length > 0) && emails.value.every(e => e.length <= 254 && /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(e)));
const role = ref('member');
const roles = [{label:'管理员',value:'admin'}, {label:'普通成员',value:'member'}];
const days = ref<number|null>(7);
const created = ref<WorkspaceInvitation|null>(null);
const busy = ref(false);
const modalError = ref('');
const rows = ref<WorkspaceInvitation[]>([]);
const page = ref(1), total = ref(0);
const listError = ref(''), loading = ref(false);
const retryTarget = ref<WorkspaceInvitation|null>(null);
const sharedRevoke = ref<WorkspaceInvitation|null>(null);
const delivery: Record<string,string> = {pending:'等待发送',sending:'正在发送',sent:'已发送',failed:'发送失败',unknown:'发送结果未知',cancelled:'已取消发送',not_applicable:'无投递记录'};
const status: Record<string,string> = {pending:'待接受',valid:'待接受',revoked:'已撤销',expired:'已过期',accepted:'已接受'};
const statusClass = (value: string) => {
  if (value === 'accepted') return 'success';
  if (value === 'pending' || value === 'valid') return 'info';
  if (value === 'revoked') return 'error';
  return 'muted';
};
const deliveryClass = (item: WorkspaceInvitation) => {
  if (item.kind === 'link') return 'muted';
  if (item.delivery_status === 'sent') return 'success';
  if (item.delivery_status === 'pending' || item.delivery_status === 'sending') return 'warning';
  if (item.delivery_status === 'failed' || item.delivery_status === 'unknown') return 'error';
  return 'muted';
};
const errorText = (e: unknown) => e instanceof Error ? e.message : '请求失败';
const canAct = (i: WorkspaceInvitation) => ['valid','pending'].includes(i.status) && (props.owner || i.role === 'member');
watch(open, value => {
  if (!value) return;
  addresses.value = ''; role.value = 'member'; days.value = 7; created.value = null; modalError.value = ''; copiedLink.value = false;
});
let request = 0;
async function load() {
  const seq = ++request;
  listError.value = ''; loading.value = true;
  try {
    const result = await workspaceSettings.invitations(props.workspace, page.value, signal);
    if (seq === request && !signal.aborted) { rows.value = result.items; total.value = result.total; }
  } catch (e) { if (seq === request && !signal.aborted) listError.value = errorText(e); }
  finally { if (seq === request) loading.value = false; }
}
watch(page, load, {immediate:true});
const valid = computed(() => validEmails.value && days.value !== null && Number.isInteger(days.value) && days.value >= 1 && days.value <= 180);
async function generate() {
  if (!valid.value || busy.value || created.value) return;
  busy.value = true; modalError.value = '';
  try {
    created.value = await workspaceSettings.invite(props.workspace, {
      kind: targeted.value ? 'email' : 'link', role: targeted.value && props.owner ? role.value : 'member',
      expires_in_days: days.value!, ...(targeted.value ? {emails: emails.value} : {})
    }, signal);
    if (page.value === 1) await load(); else page.value = 1;
  } catch (e) { if (!signal.aborted) modalError.value = errorText(e); }
  finally { busy.value = false; }
}
function selectInviteInput() {
  const inputEl = inviteBoxRef.value?.querySelector('input') ?? null;
  if (!inputEl) return;
  inputEl.focus({ preventScroll: true });
  inputEl.select();
  inputEl.setSelectionRange?.(0, inputEl.value.length);
}
async function copy() {
  const link = created.value?.link || '';
  if (!link) return;
  const inputEl = inviteBoxRef.value?.querySelector('input') ?? null;
  try {
    await copyText(link, inputEl);
    copiedLink.value = true;
    clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => { copiedLink.value = false; }, 2400);
    toast.success('已复制链接');
  } catch {
    selectInviteInput();
    toast.warning('请选中链接手动复制');
  }
}
async function update(i: WorkspaceInvitation, action: 'retry'|'revoke') {
  if (busy.value) return;
  busy.value = true;
  try {
    if (action === 'retry') await workspaceSettings.retry(props.workspace, i.id, i.delivery_status === 'unknown', signal);
    else await workspaceSettings.revoke(props.workspace, i.id, signal);
    retryTarget.value = null; sharedRevoke.value = null;
    toast.success(action === 'retry' ? '已提交重试' : '邀请已撤销');
    await load();
  } catch (e) { if (!signal.aborted) toast.error(errorText(e)); }
  finally { busy.value = false; }
}
</script>
<template>
  <div class="panel-top-actions">
    <div class="panel-top-copy">
      <strong>邀请记录</strong>
      <span class="sub-desc">管理待接受的通用邀请链接与定向邮件邀请。</span>
    </div>
    <NButton size="small" secondary :loading="loading" @click="load">刷新状态</NButton>
  </div>
  <NAlert v-if="listError" type="error">{{ listError }} <NButton text @click="load">重试</NButton></NAlert>
  <div class="members-table-card">
    <table class="custom-d2-table">
      <thead><tr><th>受邀邮箱 / 方式</th><th>工作区角色</th><th>邀请状态</th><th>到期时间</th><th>邮件状态</th><th class="col-actions">操作</th></tr></thead>
      <tbody>
        <tr v-if="!rows.length && !listError"><td colspan="6" style="padding: 32px 0;"><NEmpty :description="loading ? '正在加载邀请…' : '暂无邀请记录'" /></td></tr>
        <tr v-for="i in rows" :key="i.id">
          <td>
            <div class="invite-target-cell">
              <code v-if="i.email" class="mono-text">{{ i.email }}</code>
              <span v-else class="neutral-pill">通用邀请链接</span>
              <span v-if="i.email" class="neutral-pill">定向邮件</span>
            </div>
          </td>
          <td>
            <span class="role-badge" :class="i.role === 'admin' ? 'role-badge--admin' : 'role-badge--member'">
              {{ i.role === 'admin' ? '管理员' : '普通成员' }}
            </span>
          </td>
          <td>
            <span class="status-pill" :class="'status-pill--' + statusClass(i.status)">
              <span class="status-dot" :class="'dot--' + statusClass(i.status)"></span>
              <span>{{ status[i.status] || i.status }}</span>
            </span>
          </td>
          <td><span class="mono-text tabular-num muted-text">{{ new Date(i.expires_at).toLocaleString() }}</span></td>
          <td>
            <div class="delivery-cell">
              <span class="status-pill" :class="'status-pill--' + deliveryClass(i)">
                <span class="status-dot" :class="'dot--' + deliveryClass(i)"></span>
                <span>{{ i.kind === 'link' ? '无需邮件' : delivery[i.delivery_status] || i.delivery_status }}</span>
              </span>
              <small v-if="i.kind === 'email' && !mailEnabled" class="muted-text">（邮件未启用）</small>
            </div>
          </td>
          <td class="col-actions">
            <div class="table-action-btns">
              <NButton v-if="mailEnabled && canAct(i) && ['unknown','failed'].includes(i.delivery_status)" size="tiny" secondary :disabled="busy" @click="retryTarget = i">重试发送</NButton>
              <NButton v-if="canAct(i)" size="tiny" type="error" quaternary :disabled="busy" @click="i.shared ? sharedRevoke = i : update(i,'revoke')">撤销</NButton>
              <span v-if="!canAct(i)" class="muted-text">-</span>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
  <NPagination v-if="total > 20" v-model:page="page" :item-count="total" :page-size="20" />
  <NModal :show="open" @update:show="v => { if (!busy) open = v }" preset="card" title="邀请团队成员加入工作区" style="width:min(520px,94vw)">
    <NForm label-placement="top">
      <NFormItem v-if="mailEnabled" label="受邀邮箱（可选）">
        <NInput v-model:value="addresses" type="textarea" :autosize="{ minRows: 2, maxRows: 5 }" :disabled="busy || !!created" placeholder="多个邮箱用逗号或换行分隔" />
      </NFormItem>
      <div class="scope-notice-strip scope-notice-strip--subtle" style="margin-bottom: 14px;">
        <p class="notice-text">{{ targeted ? '同一链接仅限这些邮箱验证后分别加入。' : '通用链接可供多人注册或登录后以普通成员身份加入。' }}</p>
      </div>
      <NFormItem v-if="targeted && owner" label="分配初始角色"><NSelect v-model:value="role" :options="roles" :disabled="busy || !!created" /></NFormItem>
      <NFormItem label="有效期（天）"><NInputNumber v-model:value="days" :min="1" :max="180" :precision="0" :disabled="busy || !!created" style="width: 100%;" /></NFormItem>
      <NAlert v-if="!validEmails" type="warning">请输入有效的邮箱地址。</NAlert>
      <NAlert v-if="modalError" type="error">{{ modalError }}</NAlert>
      <div v-if="created" ref="inviteBoxRef" class="invite-link-box">
        <p>{{ created.kind === 'email' ? '邀请已创建，各邮箱的投递状态可在邀请记录中查看。' : '邀请已创建。' }}链接仅在本次生成后显示，请复制保存。</p>
        <div class="inline-input-copy">
          <NInput :value="created.link || ''" readonly @click="selectInviteInput" />
          <NButton size="small" type="primary" secondary @click="copy">{{ copiedLink ? '已复制' : '复制链接' }}</NButton>
        </div>
      </div>
    </NForm>
    <template #footer>
      <div class="modal-footer-actions">
        <NButton :disabled="busy" @click="open = false">关闭</NButton>
        <NButton v-if="!created" type="primary" :disabled="!valid" :loading="busy" @click="generate">生成邀请链接</NButton>
      </div>
    </template>
  </NModal>
  <NModal :show="!!retryTarget" @update:show="v => {if (!v && !busy) retryTarget = null}" preset="card" title="重试邀请邮件" style="width:min(460px,94vw)">
    <div class="modal-body-stack">
      <div class="scope-notice-strip scope-notice-strip--subtle">
        <code class="mono-text">{{ retryTarget?.email }}</code>
      </div>
      <p class="modal-body-copy">{{ retryTarget?.delivery_status === 'unknown' ? '上次结果未知，再次发送可能产生重复邮件。' : '将重新发送此收件人的邀请邮件。' }}</p>
    </div>
    <template #footer>
      <div class="modal-footer-actions">
        <NButton :disabled="busy" @click="retryTarget = null">取消</NButton>
        <NButton type="primary" :loading="busy" @click="retryTarget && update(retryTarget,'retry')">确认重试</NButton>
      </div>
    </template>
  </NModal>
  <NModal :show="!!sharedRevoke" @update:show="v => {if (!v && !busy) sharedRevoke = null}" preset="card" title="撤销共用链接" style="width:min(460px,94vw)">
    <div class="modal-body-stack">
      <p class="modal-body-copy">此链接下所有待接受的邀请将失效，已加入的成员仍可访问工作区。</p>
    </div>
    <template #footer>
      <div class="modal-footer-actions">
        <NButton :disabled="busy" @click="sharedRevoke = null">取消</NButton>
        <NButton type="error" :loading="busy" @click="sharedRevoke && update(sharedRevoke,'revoke')">撤销共用链接</NButton>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.invite-target-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.delivery-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
@media (max-width: 760px) {
  .custom-d2-table {
    min-width: 680px;
  }
}
</style>

<style scoped src="./workspace-settings-shared.css"></style>
