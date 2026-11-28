<script setup lang="ts">
import { computed } from 'vue';
import { NFormItem, NInput, NInputNumber, NSelect } from 'naive-ui';
import { cronClock, describeCron, readBasicCron, writeBasicCron, type BasicCron, type CronMode } from './trigger-cron';

const props = defineProps<{ value: string }>();
const emit = defineEmits<{ 'update:value': [string] }>();
const parsed = computed(() => readBasicCron(props.value));
const choices = [
  {value:'hourly',label:'每小时'},{value:'daily',label:'每天'},
  {value:'weekly',label:'每周'},{value:'monthly',label:'每月'},
];
const weekdays = ['周一','周二','周三','周四','周五','周六','周日'].map((label,i) => ({value:(i+1)%7,label}));
const dates = Array.from({length:31},(_,i) => ({value:i+1,label:`${i+1} 日`}));
function setMode(mode: CronMode) {
  const current = parsed.value ?? {mode:'daily',hour:9,minute:0,days:[]};
  emit('update:value', writeBasicCron({...current, mode, days: mode === current.mode ? current.days : [1]}));
}
function update(patch: Partial<BasicCron>) {
  if (parsed.value) emit('update:value', writeBasicCron({...parsed.value, ...patch}));
}
function setDays(value: unknown) {
  if (Array.isArray(value) && value.length && value.every(v => typeof v === 'number')) update({days:value});
}
function setTime(event: Event) {
  const value = (event.target as HTMLInputElement).value;
  const match = /^([01]\d|2[0-3]):([0-5]\d)$/.exec(value);
  if (match) update({hour:Number(match[1]),minute:Number(match[2])});
}
</script>

<template>
  <div class="cron-fields">
    <NFormItem label="重复方式">
      <NSelect :value="parsed?.mode ?? null" :options="choices" :virtual-scroll="false"
        :placeholder="value ? '当前使用高级规则' : '请选择重复方式'" @update:value="setMode" />
    </NFormItem>
    <NFormItem v-if="parsed?.mode === 'hourly'" label="每小时第几分">
      <NInputNumber :value="parsed.minute" :min="0" :max="59" :precision="0"
        @update:value="v => { if (v !== null) update({minute:v}) }" />
    </NFormItem>
    <NFormItem v-else-if="parsed" label="执行时间">
      <input id="trigger-cron-time" class="cron-time-input" type="time" step="60" aria-label="执行时间"
        :value="cronClock(parsed)" @change="setTime" />
    </NFormItem>
    <NFormItem v-if="parsed?.mode === 'weekly'" label="选择星期">
      <NSelect multiple :value="parsed.days" :options="weekdays" :virtual-scroll="false"
        @update:value="setDays" />
    </NFormItem>
    <NFormItem v-if="parsed?.mode === 'monthly'" label="选择日期">
      <NSelect multiple :value="parsed.days" :options="dates" :virtual-scroll="false"
        @update:value="setDays" />
    </NFormItem>
    <p class="cron-meaning" aria-live="polite">{{ describeCron(value) }}</p>
    <details class="cron-advanced">
      <summary>高级 Cron 表达式</summary>
      <NFormItem label="Cron（分 时 日 月 周）">
        <NInput :value="value" maxlength="200" placeholder="0 9 * * *" @update:value="v => emit('update:value',v)" />
      </NFormItem>
      <p>保存时由后端校验；同时限制日期和星期时，满足任一条件即可。</p>
    </details>
  </div>
</template>

<style scoped>
.cron-fields { display: grid; gap: 6px; }
.cron-time-input { width: 100%; min-height: 34px; padding: 4px 12px; border: 1px solid var(--id-border, #d9d9df); border-radius: 6px; background: transparent; color: inherit; }
.cron-meaning, .cron-advanced p { margin: 0; color: var(--id-text-secondary); }
.cron-advanced { padding: 8px 0; }
.cron-advanced summary { cursor: pointer; margin-bottom: 8px; }
</style>
