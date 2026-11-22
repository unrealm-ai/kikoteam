<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import {
  NAlert, NButton, NDrawer, NDrawerContent, NForm, NFormItem, NInput,
  NEmpty, NUpload, NUploadDragger, NRadioGroup, NRadio, NPagination, NSkeleton, useMessage
} from 'naive-ui';
import { activeWorkspaceId } from '../api/client';
import { skillsApi, type Skill, type SkillImport, type SkillImportInput, type SkillVersion } from '../api/capabilities';
import { useResourcePanel, useDraftClose, bytesBase64, base64Bytes } from '../composables/useResourcePanel';

import ResourceCatalogToolbar from '../components/ResourceCatalogToolbar.vue';
import ResourceOverview from '../components/ResourceOverview.vue';
import ResourceCard from '../components/ResourceCard.vue';
import { resourceSummary, type SummaryLanguage } from '../api/resource-display';
import CatalogDrawer from './ResourceCatalogDrawer.vue';
const catalogItem=ref<Skill>();
const catalogScope=ref('all');
const query = ref('');
const language = defineModel<SummaryLanguage>('language', { default: 'zh' });
const overview = ref<Skill>();
const props = defineProps<{ platformAdmin?:boolean; targetId?: string | undefined }>();
const message = useMessage();
const sources: Record<string, string> = { github: '公开 GitHub', zip: 'ZIP', markdown: 'Markdown', manual: '手动填写', agent_template: '员工模板', official:'官方发布', copy:'另存副本' };
const editableSources = ['github', 'zip', 'markdown', 'manual'] as const;
const renameOnly = ref(false);
const sourceReady = ref(false);
const { items, loading, error, page, total, canManage, load } = useResourcePanel('Skill', (w,p,signal)=>skillsApi.list(w,p,signal,catalogScope.value,query.value.trim()));
watch([catalogScope, query],()=>{if(page.value!==1)page.value=1;else void load()});
const show = ref(false);
const existing = ref<Skill>();
const fresh = () => ({
  source: 'github' as SkillImportInput['source'],
  name: '',
  description: '',
  body: '',
  markdown: '',
  repository_url: '',
  ref: '',
  subpath: '',
  version_label: ''
});
const form = ref(fresh());
const baseline = ref('');
const zip = ref('');
const zipName = ref('');
const receipt = ref<SkillImport>();
const importing = ref(false);
const saving = ref(false);
const detailError = ref('');
const busy = computed(() => importing.value || saving.value);
const dirty = computed(() => JSON.stringify(form.value) !== baseline.value || !!zip.value);
let generation = 0;
let importController: AbortController | undefined;
const now = ref(Date.now());
const timer = setInterval(() => { now.value = Date.now(); }, 1000);
const canPublish = computed(() => canManage.value && !busy.value && !!form.value.name.trim() && (
  renameOnly.value ? form.value.name.trim() !== existing.value?.name :
    sourceReady.value && !!receipt.value && Date.parse(receipt.value.expires_at) > now.value
));
function invalidate() { ++generation; importController?.abort(); importing.value = false; receipt.value = undefined; }
watch(() => [form.value.source, form.value.description, form.value.body, form.value.markdown, form.value.repository_url, form.value.ref, form.value.subpath, form.value.source === 'manual' ? form.value.name : '', zip.value], invalidate, { flush: 'sync' });
function discard() { invalidate(); show.value = false; zip.value = ''; detailError.value = ''; }
async function open(item?: Skill, rename = false) {
  discard();
  existing.value = item;
  renameOnly.value = rename;
  sourceReady.value = !item;
  form.value = { ...fresh(), name: item?.name || '' };
  baseline.value = JSON.stringify(form.value);
  zipName.value = '';
  show.value = true;
  if (!item || rename) return;

  // Older clients could change the import source. The first published version owns it.
  const current = generation;
  try {
    const origin = item.current_version.sequence === 1 ? item.current_version :
      (await skillsApi.versions(activeWorkspaceId.value, item.id)).items.find(v => v.sequence === 1);
    if (current !== generation) return;
    const source = origin?.source.type === 'copy' ? 'zip' : editableSources.find(s => s === origin?.source.type);
    if (!origin || !source) { detailError.value = '该来源无法在资源库重新上传。'; return; }
    const previous = item.current_version.source.type === source ? item.current_version : origin;
    Object.assign(form.value, {
      source, repository_url: previous.source.repository_url || '', ref: previous.source.ref || '',
      subpath: previous.source.subpath || '',
      ...(source === 'manual' ? {name: previous.technical_name, description: previous.description} : {})
    });
    baseline.value = JSON.stringify(form.value);
    sourceReady.value = true;
  } catch (e) {
    if (current === generation) detailError.value = e instanceof Error ? e.message : '读取原始导入方式失败';
  }
}
async function chooseZip(file: File) {
  zip.value = ''; zipName.value = ''; invalidate();
  const current = generation; const w = activeWorkspaceId.value; detailError.value = '';
  if (file.size > 20 * 1024 * 1024) { detailError.value = 'ZIP 文件不得超过 20 MiB。'; return; }
  try {
    const bytes = new Uint8Array(await file.arrayBuffer());
    if (current !== generation || w !== activeWorkspaceId.value || !show.value) return;
    zip.value = bytesBase64(bytes); zipName.value = file.name;
  } catch { if (current === generation) detailError.value = '无法读取 ZIP 文件，请重新选择。'; }
}
async function chooseMarkdown(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0];
  if (!file) return;
  form.value.markdown = ''; invalidate();
  const current = generation, w = activeWorkspaceId.value; detailError.value = '';
  if (file.size > 64 * 1024) { detailError.value = 'Markdown 文件不得超过 64 KiB。'; return; }
  try {
    const text = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer());
    if (current === generation && w === activeWorkspaceId.value && show.value) form.value.markdown = text;
  } catch { if (current === generation) detailError.value = '无法读取 UTF-8 Markdown 文件。'; }
}
async function prepare() {
  if (!canManage.value || busy.value || !sourceReady.value || renameOnly.value) return;
  invalidate(); detailError.value = '';
  const current = generation; const w = activeWorkspaceId.value; const f = form.value;
  const payload: SkillImportInput = { source: f.source };
  if (f.source === 'zip') { if (!zip.value) { detailError.value = '请选择 ZIP 文件。'; return; } payload.zip_base64 = zip.value; }
  else if (f.source === 'github') { if (!f.repository_url.trim()) { detailError.value = '请填写公开 GitHub 仓库地址。'; return; } Object.assign(payload, { repository_url: f.repository_url.trim(), ref: f.ref.trim(), subpath: f.subpath.trim() }); }
  else if (f.source === 'markdown') payload.markdown = f.markdown;
  else Object.assign(payload, { name: f.name.trim(), description: f.description.trim(), body: f.body });
  importing.value = true; importController = new AbortController();
  const timeout = setTimeout(() => importController?.abort(), 120000);
  try {
    const result = await skillsApi.import(w, payload, importController.signal);
    if (current !== generation || w !== activeWorkspaceId.value || !show.value) return;
    if (!f.name && f.source !== 'manual') f.name = result.technical_name;
    receipt.value = result; now.value = Date.now();
  } catch (e) {
    if (current === generation) detailError.value = e instanceof Error && e.name === 'AbortError' ? '导入已取消或超时；未发布任何版本。' : e instanceof Error ? e.message : '导入失败';
  } finally { clearTimeout(timeout); if (current === generation) importing.value = false; }
}
async function publish() {
  if (!canPublish.value) return false;
  const current = generation, w = activeWorkspaceId.value;
  saving.value = true; detailError.value = '';
  try {
    if (renameOnly.value && existing.value) {
      await skillsApi.update(w, existing.value, { name: form.value.name.trim() });
    } else if (receipt.value && Date.parse(receipt.value.expires_at) > Date.now()) {
      await skillsApi.publish(w, receipt.value.id, form.value.name.trim(), form.value.version_label.trim(), existing.value);
    } else return false;
    if (current !== generation || w !== activeWorkspaceId.value) return false;
    discard(); message.success(renameOnly.value ? '展示名已更新' : 'Skill 完整目录版本已保存'); await load(); return true;
  } catch (e) { if (current === generation) detailError.value = e instanceof Error ? e.message : '发布失败'; return false; }
  finally { saving.value = false; }
}
const close = useDraftClose('Skill 导入', show, dirty, busy, discard, publish);
const actionBusy = ref(false);
async function toggle(item: Skill) {
  if (!canManage.value || actionBusy.value) return;
  const w = activeWorkspaceId.value; actionBusy.value = true;
  try {
    await skillsApi.update(w, item, { enabled: !item.enabled });
    if (w === activeWorkspaceId.value) { message.success('Skill 状态已更新'); await load(); }
  } catch (e) { if (w === activeWorkspaceId.value) message.error(e instanceof Error ? e.message : '更新失败'); }
  finally { actionBusy.value = false; }
}
const viewing = ref<Skill>();
const history = ref<SkillVersion[]>([]);
const versionID = ref('');
const historyLoading = ref(false);
const historyError = ref('');
const content = ref('');
let historyEpoch = 0;
const instructionView = ref(false);
const fileLoading = ref(false);
const chosenVersion = computed(() => history.value.find(v => v.id === versionID.value));
async function inspect(item: Skill, instruction = false) {
  const current = ++historyEpoch, w = activeWorkspaceId.value;
  viewing.value = item;
  instructionView.value = instruction;
  history.value = [];
  historyError.value = '';
  fileLoading.value = false;
  content.value = '';
  if (instruction) {
    historyLoading.value = false;
    history.value = [item.current_version];
    versionID.value = item.current_version_id;
    await preview('SKILL.md');
    return;
  }
  historyLoading.value = true;
  try {
    const result = await skillsApi.versions(w, item.id);
    if (current !== historyEpoch || w !== activeWorkspaceId.value) return;
    history.value = result.items;
    historyLoading.value = false;
    versionID.value = item.current_version_id;
  } catch (e) {
    if (current === historyEpoch) {
      historyError.value = e instanceof Error ? e.message : '版本读取失败';
      historyLoading.value = false;
    }
  }
}
async function preview(path: string) {
  if (!viewing.value || !chosenVersion.value) return;
  const current = ++historyEpoch, w = activeWorkspaceId.value;
  content.value = ''; historyError.value = ''; fileLoading.value = true;
  try {
    const version = chosenVersion.value;
    const file = version.manifest.files.find(entry => entry.path === path);
    if (file && file.size > 1024 * 1024) { content.value = '文件超过 1 MiB，不提供内联预览。'; return; }
    const f = await skillsApi.file(w, viewing.value.id, version.id, path);
    if (current !== historyEpoch || w !== activeWorkspaceId.value) return;
    if (f.size > 1024 * 1024) { content.value = '文件超过 1 MiB，不提供内联预览。'; return; }
    try { content.value = new TextDecoder('utf-8', { fatal: true }).decode(base64Bytes(f.content_base64)); }
    catch { content.value = '此文件为二进制附件，不提供文本预览。'; }
  } catch (e) { if (current === historyEpoch) historyError.value = e instanceof Error ? e.message : '文件读取失败'; }
  finally { if (current === historyEpoch) fileLoading.value = false; }
}
function closeHistory(value: boolean) { if (!value) { viewing.value = undefined; ++historyEpoch; } }
watch(versionID, () => { ++historyEpoch; content.value = ''; fileLoading.value = false; historyError.value = ''; }, {flush: 'sync'});
watch(() => [activeWorkspaceId.value, props.targetId] as const, async ([w, id], _, onCleanup) => {
  closeHistory(false);
  if (!w || !id) return;
  const controller = new AbortController();
  onCleanup(() => controller.abort());
  try {
    const skill = await skillsApi.get(w, id, controller.signal);
    if (!controller.signal.aborted) await inspect(skill, true);
  } catch (e) {
    if (!controller.signal.aborted) message.error(e instanceof Error ? e.message : 'Skill 不可用');
  }
}, {immediate: true});
watch(activeWorkspaceId, () => { overview.value=undefined; catalogItem.value=undefined; discard(); ++historyEpoch; viewing.value = undefined; saving.value = false; });
onUnmounted(() => { invalidate(); ++historyEpoch; clearInterval(timer); });
</script>

<template>
  <section class="tab-content-panel">
    <ResourceCatalogToolbar v-model:scope="catalogScope" v-model:query="query" v-model:language="language"
      kind="技能" :platform-admin="!!platformAdmin" :total="total" :loading="loading">
      <NButton v-if="canManage" size="small" type="primary" @click="open()">+ 导入技能</NButton>
    </ResourceCatalogToolbar>
    <ResourceOverview v-if="overview" :item="overview" :language="language" @close="overview=undefined">
        <NButton v-if="canManage || platformAdmin" type="primary" size="small" @click="catalogItem=overview; overview=undefined">分配与管理</NButton>
        <NButton size="small" @click="inspect(overview); overview=undefined">版本历史</NButton>
        <NButton size="small" @click="inspect(overview,true); overview=undefined">查看指令</NButton>
        <template v-if="canManage && overview.scope !== 'official'">
          <NButton size="small" @click="open(overview,true); overview=undefined">改名</NButton>
          <NButton size="small" @click="open(overview); overview=undefined">上传新版本</NButton>
        </template>
        <NButton v-if="canManage && overview.scope !== 'official'" size="small" :disabled="actionBusy" @click="toggle(overview); overview=undefined">{{ overview.enabled ? '停用' : '启用' }}</NButton>
    </ResourceOverview>
    <CatalogDrawer v-if="catalogItem" :key="catalogItem.id" kind="skill" :item="catalogItem" :platform-admin="!!platformAdmin" :can-manage="canManage" @close="catalogItem=undefined" @changed="load" />

    <div v-if="loading" class="resource-loading">
      <div v-for="n in 3" :key="'sk-sk-' + n" class="resource-skeleton-card">
        <NSkeleton text style="width: 55%; height: 18px;" />
        <NSkeleton style="height: 60px; border-radius: 6px;" />
        <NSkeleton text style="width: 85%; height: 22px;" />
      </div>
    </div>
    <NAlert v-else-if="error" type="error">
      {{ error }}
      <NButton size="small" style="margin-left: 8px;" @click="load">重试</NButton>
    </NAlert>
    <template v-else>
      <NAlert v-if="!canManage" type="info">当前仅可查看，资源管理需要管理员权限。</NAlert>
      <div v-if="!items.length" class="empty-panel-box">
        <NEmpty description="暂无匹配的技能">
          <template v-if="canManage" #extra><NButton size="small" type="primary" @click="open()">+ 导入技能</NButton></template>
        </NEmpty>
      </div>
      <div v-else class="catalog-card-grid">
        <ResourceCard v-for="s in items" :key="s.id" :item="s" :language="language"
          :class="{ highlighted: targetId === s.id }" :global="catalogScope === 'manage'" @detail="overview=s" />
      </div>
      <div v-if="total > 20" class="resource-pagination">
        <NPagination v-model:page="page" :page-size="20" :item-count="total" />
      </div>
    </template>

    <NDrawer class="library-drawer" :show="show" :width="'min(620px,95vw)'" :mask-closable="false" :close-on-esc="false">
      <NDrawerContent>
        <template #header>
          <div class="resource-heading">
            <strong>{{ renameOnly ? '修改 Skill 展示名' : existing ? '上传 Skill 新版本' : '导入 / 创建 Skill' }}</strong>
            <NButton size="small" quaternary :disabled="saving" @click="close">关闭</NButton>
          </div>
        </template>
        <NForm label-placement="top" :disabled="busy">
          <template v-if="!renameOnly">
            <NFormItem v-if="!existing" label="导入方式">
              <NRadioGroup v-model:value="form.source" size="small">
                <div class="resource-inline-row">
                  <NRadio value="zip">ZIP</NRadio>
                  <NRadio value="github">GitHub</NRadio>
                  <NRadio value="markdown">Markdown</NRadio>
                </div>
              </NRadioGroup>
            </NFormItem>

            <p v-if="existing && !sourceReady && !detailError" role="status">正在读取原始导入方式…</p>
            <p v-if="existing && sourceReady" class="field-note">原始导入方式 · {{ sources[form.source] }}</p>
            <template v-if="sourceReady">
              <div v-if="form.source === 'zip'" class="import-mode-block">
                <NFormItem label="完整 Skill ZIP（最多 20 MiB）">
                  <NUpload :show-file-list="false" :default-upload="false" accept=".zip" :disabled="busy"
                    @change="({ file }) => file.file && chooseZip(file.file)">
                    <NUploadDragger>点击或拖拽上传 .zip 格式 Skill 资源包</NUploadDragger>
                  </NUpload>
                </NFormItem>
                <p v-if="zipName" class="resource-hint-sm">已选择：<strong>{{ zipName }}</strong></p>
              </div>

              <div v-if="form.source === 'github'" class="import-mode-block">
                <NFormItem label="公开 GitHub 仓库"><NInput v-model:value="form.repository_url" placeholder="https://github.com/owner/repo" /></NFormItem>
                <div class="res-grid-2">
                  <NFormItem label="分支、标签或 commit（选填）"><NInput v-model:value="form.ref" /></NFormItem>
                  <NFormItem label="仓库内目录（选填）"><NInput v-model:value="form.subpath" placeholder="skills/example" /></NFormItem>
                </div>
                <p class="resource-hint-sm">导入时固定实际 commit；私有仓库授权尚未开放。</p>
              </div>

              <div v-if="form.source === 'markdown'" class="import-mode-block">
                <NFormItem label="导入 Markdown 文件（或在下方粘贴）">
                  <input type="file" accept=".md,.markdown,text/markdown" class="resource-file-input" :disabled="busy" @change="chooseMarkdown" />
                </NFormItem>
                <NFormItem label="Markdown 内容">
                  <NInput v-model:value="form.markdown" type="textarea" :autosize="{ minRows: 8, maxRows: 20 }" placeholder="粘贴包含 name / description 的 SKILL.md" />
                </NFormItem>
              </div>

            </template>
          </template>
          <NFormItem :label="!renameOnly && form.source === 'manual' ? 'Skill 技术名称' : '展示名称'">
            <NInput v-model:value="form.name" :disabled="!!existing && !renameOnly && form.source !== 'manual'" :maxlength="100" placeholder="Skill 名称" />
          </NFormItem>
          <template v-if="!renameOnly && sourceReady && form.source === 'manual'">
            <NFormItem label="说明"><NInput v-model:value="form.description" /></NFormItem>
            <NFormItem label="技能正文"><NInput v-model:value="form.body" type="textarea" :autosize="{ minRows: 8, maxRows: 20 }" /></NFormItem>
          </template>
          <NFormItem v-if="!renameOnly && sourceReady" label="版本标签（选填）">
            <NInput v-model:value="form.version_label" placeholder="仅作为展示，不替代系统版本 ID" />
          </NFormItem>
        </NForm>

        <NAlert v-if="detailError" class="resource-alert" type="error">{{ detailError }}</NAlert>
        <div v-if="!renameOnly && sourceReady" class="resource-inline-row" style="margin-top: 4px;">
          <NButton type="primary" secondary :loading="importing" :disabled="busy || !canManage" @click="prepare">校验并预览</NButton>
          <NButton v-if="importing" @click="invalidate">取消导入</NButton>
        </div>

        <template v-if="receipt">
          <NAlert class="resource-alert" :type="Date.parse(receipt.expires_at) > now ? 'success' : 'warning'">
            {{ Date.parse(receipt.expires_at) > now ? '目录已校验，请确认后发布。' : '导入预览已过期，请重新校验。' }} {{ receipt.technical_name }}
          </NAlert>
          <p v-if="receipt.source.commit" class="resource-hint-sm" style="margin-bottom: 6px;">Commit：<code class="mono-pill">{{ receipt.source.commit }}</code></p>
          <ul class="resource-files">
            <li v-for="file in receipt.manifest.files" :key="file.path">{{ file.path }} · {{ file.size }} bytes</li>
          </ul>
        </template>

        <template #footer>
          <div class="resource-drawer-footer">
            <NButton :disabled="saving" @click="close">取消</NButton>
            <NButton type="primary" :disabled="!canPublish" :loading="saving" @click="publish">{{ renameOnly ? '保存名称' : existing ? '发布新版本' : '保存 Skill' }}</NButton>
          </div>
        </template>
      </NDrawerContent>
    </NDrawer>

    <NDrawer class="library-drawer" :show="!!viewing" :width="instructionView ? 'min(540px,95vw)' : 'min(560px,95vw)'" @update:show="closeHistory">
      <NDrawerContent v-if="viewing" :title="(instructionView ? 'Skill 详情 · ' : '历史版本归档 · ') + viewing.name" closable>
        <NAlert v-if="historyError" type="error" class="resource-alert">{{ historyError }}</NAlert>
        <div v-if="instructionView" class="skill-drawer-content">
          <div class="sk-info-row">
            <span>当前版本 <strong>v{{ viewing.current_version.sequence }}</strong></span>
            <span>{{ sources[viewing.current_version.source.type || ''] || viewing.current_version.source.type }}</span>
            <code v-if="viewing.current_version.source.commit">{{ viewing.current_version.source.commit }}</code>
          </div>
          <div class="sk-desc-box">{{ resourceSummary(viewing,language) }}</div>
          <div class="sk-prompt-section">
            <div class="sk-prompt-header">SKILL.md</div>
            <p v-if="fileLoading" role="status">正在读取指令…</p>
            <pre v-else-if="!historyError" class="sk-prompt-body">{{ content }}</pre>
          </div>
        </div>
        <div v-else class="skill-history-drawer-body">
          <p v-if="historyLoading" role="status">正在读取版本…</p>
          <div class="history-version-cards">
            <article v-for="v in history" :key="v.id" class="history-ver-card">
              <div class="hvc-top">
                <div class="hvc-ver-title">
                  <span class="v-tag">v{{ v.sequence }}</span><span v-if="v.version_label">{{ v.version_label }}</span>
                  <span class="neutral-pill">{{ sources[v.source.type || ''] || v.source.type }}</span>
                  <code v-if="v.source.commit" class="hvc-commit" :title="v.source.commit">{{ v.source.commit.slice(0,8) }}</code>
                </div>
                <span class="hvc-time">{{ new Date(v.created_at).toLocaleString() }}</span>
              </div>
              <p class="hvc-log">{{ v.description }}</p>
              <NButton size="tiny" secondary :aria-expanded="versionID === v.id" @click="versionID = versionID === v.id ? '' : v.id">
                {{ versionID === v.id ? '收起文件' : '查看文件' }}（{{ v.manifest.files.length }}）
              </NButton>
              <template v-if="versionID === v.id">
                <ul class="resource-files"><li v-for="file in v.manifest.files" :key="file.path">
                  <button type="button" @click="preview(file.path)">{{ file.path }} · {{ file.size }} bytes</button>
                </li></ul>
                <p v-if="fileLoading" role="status">正在读取文件…</p>
                <pre v-else-if="content" class="sk-prompt-body">{{ content }}</pre>
              </template>
            </article>
          </div>
        </div>
      </NDrawerContent>
    </NDrawer>
  </section>
</template>

<style src="../styles/platform-resources.css"></style>
