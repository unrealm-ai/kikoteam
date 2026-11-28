import fs from 'node:fs';
import { createHash } from 'node:crypto';
import {loadBank} from './bank.mjs';
import { historicalFixture } from '../fixtures/historical.mjs';
import { followupFixture } from '../fixtures/followup.mjs';
import { names, prefix } from './fixtures.mjs';

const manager = new URL('../../backend/', import.meta.url);
const engine = new URL('../../agent-engine/', import.meta.url);
const catalog = JSON.parse(fs.readFileSync(new URL('contracts/taskdelegation/tools.json', manager)));
const goPrompt = fs.readFileSync(new URL('internal/manager/service/prompts.go', manager), 'utf8')
  .match(/const PlatformChatInstruction = `([^\x60]+)`/)?.[1];
const engineCopy = fs.readFileSync(new URL('internal/runtime/configuration.go', engine), 'utf8');
const attach = engineCopy.match(/out\.Instruction \+= "\\n(主对话只知道用户上传了什么文件[^"\n]+)"/)?.[1];
const report = engineCopy.match(/const reportOnlyResponseInstruction = `([^\x60]+)`/)?.[1]
  ?? '从本轮任务回报的候选清单中选取需要交付的产物。没有候选时只回复文字。';
if (!goPrompt || !attach) throw Error('Unable to read checkout prompt; provide a supported profile');

const employee = '你是下方岗位配置中的 AI 员工，以该员工的身份工作。岗位规范约束职责、表达和协作，平台规则优先。岗位配置不授予操作或数据访问权限。\n名称 "SRE（合成评测）"\n职责 "协助查询和总结内部运维任务"';
export const digest = x => createHash('sha256').update(JSON.stringify(x, (_, v) => v instanceof RegExp ? String(v) : v)).digest('hex');
export const modelOptions = Object.freeze({ temperature: 0, max_tokens: 16384 });
export const turnDeadlineMs = 600_000;
export function loadSuite(tags=[]) {
  if (!Array.isArray(tags) || tags.some(t=>typeof t!=='string'||!/^[a-z0-9][a-z0-9_-]*$/.test(t))) throw Error('Invalid tag');
  const {cases,total,sourceCount}=loadBank(tags);
  const fixture=(sample,p)=>(sample.mock==='historical'?historicalFixture:followupFixture)(sample,p);
  const sources=['historical','followup'].map(name=>fs.readFileSync(new URL('../fixtures/'+name+'.mjs',import.meta.url),'utf8'));
  return {id:'main-chat',label:'主对话题库',cases,total,sourceCount,fixture,
    hash:digest(cases),fixtureHash:digest(sources)};
}
export function loadProfile(spec = {}) {
  const id = spec.id ?? 'checkout';
  if (!/^[a-z0-9][a-z0-9_-]{0,39}$/.test(id)) throw Error('Invalid profile ID');
  const fields = new Set(['id', 'systemPrompt', 'reportPrompt', 'toolDescriptions', 'createReceipt']);
  if (Object.keys(spec).some(x => !fields.has(x))) throw Error('Unknown profile field');
  const copy = input => typeof input === 'string' && input.trim().length > 0 && input.length <= 64_000;
  for (const k of ['systemPrompt', 'reportPrompt']) if (spec[k] !== undefined && !copy(spec[k])) throw Error('Invalid profile prompt');
  const overrides = spec.toolDescriptions ?? {};
  if (typeof overrides !== 'object' || !overrides || Array.isArray(overrides)) throw Error('Invalid tool descriptions');
  const known = new Set(catalog.map(t => t.name));
  if (Object.entries(overrides).some(([n,v]) => !known.has(n)||!copy(v))) throw Error('Invalid tool description override');
  const createReceipt = spec.createReceipt ?? 'accepted';
  if (!['legacy', 'accepted'].includes(createReceipt)) throw Error('Invalid create receipt');
  const system = spec.systemPrompt ?? goPrompt, reportCopy = spec.reportPrompt ?? report;
  const tools = catalog.map(t => ({...t, description: Object.hasOwn(overrides,t.name) ? overrides[t.name] : t.description}));
  return { id, system, reportCopy, tools, createReceipt,
    systemHash:digest(system), reportHash:digest(reportCopy), toolsHash:digest(tools) };
}
export function makeRequest(fixture, profile) {
  const channel = fixture.channel !== 'web';
  const detail = channel && fixture.taskDetail === true;
  const allowed = profile.tools.filter(t => {
    if (t.name === 'send_task_detail_link') return detail;
    if (t.name === names.artifact) return channel;
    if (fixture.reportOnly) return false;
    if (t.name === 'trigger_task_get_result') return fixture.group === true;
    return true;
  });
  return {
    messages:[{ role:'system', content:employee + '\n\n' + profile.system + '\n' + (fixture.reportOnly ? profile.reportCopy : attach) },
      ...fixture.history, { role:'user', content:fixture.input }],
    tools:allowed.map(t => ({ type:'function', function: { name:prefix + t.name, description:t.description, parameters:t.inputSchema } })),
  };
}
