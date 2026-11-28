export type CronMode = 'hourly' | 'daily' | 'weekly' | 'monthly';
export type BasicCron = { mode: CronMode; minute: number; hour: number; days: number[] };
const weekdays = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];
const pad = (value: number) => String(value).padStart(2, '0');
function numberField(value: string, min: number, max: number) {
  if (!/^\d{1,2}$/.test(value)) return undefined;
  const n = Number(value);
  return n >= min && n <= max ? n : undefined;
}
function dayFields(value: string, min: number, max: number): number[] | undefined {
  const entries = value.split(',').map(item => numberField(item, min, max));
  if (entries.some(item => item === undefined)) return undefined;
  return [...new Set(entries as number[])].sort((a, b) => a - b);
}

export function readBasicCron(raw: string): BasicCron | undefined {
  const fields = raw.trim().split(/\s+/);
  if (fields.length !== 5) return undefined;
  const [m, h, dom, month, dow] = fields as [string, string, string, string, string];
  const minute = numberField(m, 0, 59);
  if (minute === undefined || month !== '*') return undefined;
  const hour = numberField(h, 0, 23);
  if (dom === '*' && dow === '*') {
    if (h === '*') return { mode: 'hourly', minute, hour: 9, days: [] };
    if (hour !== undefined) return { mode: 'daily', minute, hour, days: [] };
  }
  if (hour === undefined) return undefined;
  const weekly = dom === '*' ? dayFields(dow, 0, 6) : undefined;
  if (weekly) return { mode: 'weekly', minute, hour, days: weekly };
  const monthly = dow === '*' ? dayFields(dom, 1, 31) : undefined;
  if (monthly) return { mode: 'monthly', minute, hour, days: monthly };
}
export function writeBasicCron(v: BasicCron): string {
  const days = [...new Set(v.days)].sort((a, b) => a - b).join(',');
  return [v.minute, v.mode === 'hourly' ? '*' : v.hour, v.mode === 'monthly' ? days : '*', '*', v.mode === 'weekly' ? days : '*'].join(' ');
}
export function cronClock(v: BasicCron) {
  return `${pad(v.hour)}:${pad(v.minute)}`;
}
export function describeCron(raw = ''): string {
  if (!raw.trim()) return '尚未设置执行时间';
  const v = readBasicCron(raw);
  if (v?.mode === 'hourly') return `每小时第 ${pad(v.minute)} 分执行`;
  if (v?.mode === 'daily') return `每天 ${cronClock(v)} 执行`;
  if (v?.mode === 'weekly') return `每周${v.days.map(day => weekdays[day]).join('、')} ${cronClock(v)} 执行`.replace('每周周', '每周');
  if (v?.mode === 'monthly') return `每月 ${v.days.join('、')} 日 ${cronClock(v)} 执行`;
  const fields = raw.trim().split(/\s+/);
  if (fields.length !== 5) return '请输入五段 Cron：分、时、日、月、周';
  const [minute, hour, day, month, weekday] = fields;
  if (hour === '*' && day === '*' && month === '*' && weekday === '*' && minute && /^\*\/(?:[2-9]|[1-5]\d)$/.test(minute)) {
    return `每小时内，每隔 ${minute.slice(2)} 分钟匹配一次`;
  }
  return `高级规则：${['分钟','小时','日期','月份','星期'].map((label, i) => `${label} ${fields[i] === '*' ? '不限' : fields[i]}`).join(' · ')}`;
}
