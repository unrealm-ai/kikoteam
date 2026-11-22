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
onUnmounted(() => controller.abort());
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
const errorText = (e: unknown) => e instanceof Error ? e.message : '请求失败';
const canAct = (i: WorkspaceInvitation) => ['valid','pending'].includes(i.status) && (props.owner || i.role === 'member');
watch(open, value => {
  if (!value) return;
  addresses.value = ''; role.value = 'member'; days.value = 7; created.value = null; modalError.value = '';
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
async function copy() {
  try { await copyText(created.value!.link!); toast.success('已复制链接'); }
  catch { toast.warning('请选中链接手动复制'); }
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
    <strong>邀请记录</strong><NButton size="small" secondary :loading="loading" @click="load">刷新状态</NButton>
  </div>
  <NAlert v-if="listError" type="error">{{ listError }} <NButton text @click="load">重试</NButton></NAlert>
  <div class="members-table-card">
    <table class="custom-d2-table">
      <thead><tr><th>受邀邮箱 / 方式</th><th>工作区角色</th><th>邀请状态</th><th>到期时间</th><th>邮件状态</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-if="!rows.length && !listError"><td colspan="6"><NEmpty :description="loading ? '正在加载邀请…' : '暂无邀请记录'" /></td></tr>
        <tr v-for="i in rows" :key="i.id">
          <td>{{ i.email || '通用邀请链接' }}</td><td>{{ i.role === 'admin' ? '管理员' : '普通成员' }}</td>
          <td>{{ status[i.status] || i.status }}</td><td>{{ new Date(i.expires_at).toLocaleString() }}</td>
          <td>{{ i.kind === 'link' ? '无需邮件' : delivery[i.delivery_status] || i.delivery_status }}<small v-if="i.kind === 'email' && !mailEnabled">（邮件未启用）</small></td>
          <td><NButton v-if="mailEnabled && canAct(i) && ['unknown','failed'].includes(i.delivery_status)" size="tiny" :disabled="busy" @click="retryTarget = i">重试发送</NButton>
            <NButton v-if="canAct(i)" text :disabled="busy" @click="i.shared ? sharedRevoke = i : update(i,'revoke')">撤销</NButton></td>
        </tr>
      </tbody>
    </table>
  </div>
  <NPagination v-if="total > 20" v-model:page="page" :item-count="total" :page-size="20" />
  <NModal :show="open" @update:show="v => { if (!busy) open = v }" preset="card" title="邀请团队成员加入工作区" style="width:min(520px,94vw)">
    <NForm label-placement="top">
      <NFormItem v-if="mailEnabled" label="受邀邮箱（可选）">
        <NInput v-model:value="addresses" type="textarea" :disabled="busy || !!created" placeholder="多个邮箱用逗号或换行分隔" />
      </NFormItem>
      <p>{{ targeted ? '同一链接仅限这些邮箱验证后分别加入。' : '通用链接可供多人注册或登录后以普通成员身份加入。' }}</p>
      <NFormItem v-if="targeted && owner" label="分配初始角色"><NSelect v-model:value="role" :options="roles" :disabled="busy || !!created" /></NFormItem>
      <NFormItem label="有效期（天）"><NInputNumber v-model:value="days" :min="1" :max="180" :precision="0" :disabled="busy || !!created" /></NFormItem>
      <NAlert v-if="!validEmails" type="warning">请输入有效的邮箱地址。</NAlert>
      <NAlert v-if="modalError" type="error">{{ modalError }}</NAlert>
      <div v-if="created" class="invite-link-box">
        <p>{{ created.kind === 'email' ? '邀请已创建，各邮箱的投递状态可在邀请记录中查看。' : '邀请已创建。' }}链接仅在本次生成后显示，请复制保存。</p>
        <NInput :value="created.link || ''" readonly /><NButton size="small" secondary @click="copy">复制链接</NButton>
      </div>
    </NForm>
    <template #footer><div class="table-action-btns"><NButton :disabled="busy" @click="open = false">关闭</NButton>
      <NButton v-if="!created" type="primary" :disabled="!valid" :loading="busy" @click="generate">生成邀请链接</NButton></div></template>
  </NModal>
  <NModal :show="!!retryTarget" @update:show="v => {if (!v && !busy) retryTarget = null}" preset="card" title="重试邀请邮件" style="width:min(460px,94vw)">
    <p>{{ retryTarget?.email }} · {{ retryTarget?.delivery_status === 'unknown' ? '上次结果未知，再次发送可能产生重复邮件。' : '将重新发送此收件人的邀请邮件。' }}</p>
    <NButton type="primary" :loading="busy" @click="retryTarget && update(retryTarget,'retry')">确认重试</NButton>
  </NModal>
  <NModal :show="!!sharedRevoke" @update:show="v => {if (!v && !busy) sharedRevoke = null}" preset="card" title="撤销共用链接" style="width:min(460px,94vw)">
    <p>此链接下所有待接受的邀请将失效，已加入的成员仍可访问工作区。</p>
    <NButton type="error" :loading="busy" @click="sharedRevoke && update(sharedRevoke,'revoke')">撤销共用链接</NButton>
  </NModal>
</template>

<style scoped src="./workspace-settings-shared.css"></style>
