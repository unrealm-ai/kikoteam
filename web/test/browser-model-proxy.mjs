import assert from 'node:assert/strict';

const { chromium } = await import(process.env.KIKO_PLAYWRIGHT_MODULE || 'playwright');
const app = process.env.KIKO_PROXY_BROWSER_URL;
const upstream = process.env.KIKO_PROXY_PROVIDER_URL;
assert.ok(app && upstream, 'Run through TestModelProxyBrowser for an isolated backend and supplier');
const channel = process.env.KIKO_PLAYWRIGHT_CHANNEL;
const browser = await chromium.launch({headless: true, ...(channel ? {channel} : {})});
const exceptions = [];

async function register(email, name) {
  const page = await browser.newPage({viewport:{width:1440,height:1040}});
  page.on('pageerror', error => exceptions.push(error.message));
  await page.goto(app);
  await page.getByText('注册', {exact:true}).click();
  await page.getByPlaceholder('例如：张明').fill(name);
  const form = page.locator('form').filter({hasText:'姓名 / 昵称'});
  await form.getByPlaceholder('name@company.com').fill(email);
  await page.getByPlaceholder('10–72 个 UTF-8 字节').fill('sample-test-password');
  const registered = page.waitForResponse(r=>r.url().endsWith('/auth/registrations'));
  await page.getByRole('button',{name:'创建账号并登录'}).click();
  assert.equal((await registered).status(),201);
  const reply = await page.request.get(app+'/api/v1/workspaces');
  assert.ok(reply.ok());
  return {page, workspace:(await reply.json()).items[0].id};
}

function field(page, label) {
  return page.locator('.n-form-item').filter({hasText:label}).locator('input').first();
}

try {
  const {page:admin,workspace:adminWorkspace}=await register('admin@browser.example','平台测试员');
  await admin.goto(`${app}/#view=platform-admin&ws=${adminWorkspace}`);
  await admin.getByRole('button',{name:'官方模型',exact:true}).click();
  await admin.getByRole('button',{name:'添加官方模型'}).click();
  await field(admin,'模型名称').fill('官方浏览器测试模型');
  await field(admin,'服务地址').fill(upstream+'/v1');
  await field(admin,'上游模型 ID').fill('actual');
  await field(admin,'供应商 API Key').fill('supplier-only');
  await field(admin,'输入价格').fill('1000000');
  await field(admin,'输出价格').fill('1000000');
  await field(admin,'缓存读取价格').fill('0');
  await admin.getByText('设为默认',{exact:true}).click();
  await admin.getByRole('button',{name:'测试连接'}).click();
  await admin.getByText('连接正常，已收到流式计费用量。').waitFor();
  await admin.getByRole('button',{name:'保存',exact:true}).click();
  await admin.getByText('官方模型已保存').waitFor();

  const {page:user,workspace}=await register('user@browser.example','新员工创建者');
  await user.goto(`${app}/#view=employees&ws=${workspace}`);
  await user.getByRole('button',{name:'新建 AI 员工'}).click();
  await user.getByPlaceholder('例如：研发助手').fill('模型测试员工');
  await user.getByText('该员工使用官方模型产生的费用将从你的积分账户扣除。').waitFor();
  const created = user.waitForResponse(r=>r.request().method()==='POST' && r.url().endsWith('/agents'));
  await user.getByRole('button',{name:'创建员工',exact:true}).click();
  const employeeReply=await created;
  assert.equal(employeeReply.status(),201);
  const employee=await employeeReply.json();
  assert.ok(employee.platform_model_id);
  assert.ok(employee.payer);
  assert.equal(employee.api_key,undefined);

  await user.goto(`${app}/#view=chat&ws=${workspace}&emp=${employee.id}`);
  const composer=user.locator('textarea').first();
  await composer.fill('请回复测试消息');
  await composer.press('Enter');
  await user.getByText('Model proxy fixture reply',{exact:true}).waitFor({timeout:30000});

  await user.goto(`${app}/#view=credits&ws=${workspace}`);
  await user.getByRole('heading',{name:'我的积分'}).waitFor();
  await user.getByText('995.000000 积分',{exact:true}).waitFor();
  await user.getByRole('button',{name:'模型调用',exact:true}).click();
  await user.getByText('已结算',{exact:true}).waitFor();
  await user.getByText('模型测试员工 · 官方浏览器测试模型',{exact:true}).waitFor();

  const base = app+'/api/v1/workspaces/'+workspace;
  const headers = {'X-CSRF-Protection':'1'};
  const conversation = await user.request.post(base+'/conversations',{headers,data:{agent_id:employee.id}});
  assert.equal(conversation.status(),201);
  const task = await user.request.post(base+'/sessions',{
    headers:{...headers,'Idempotency-Key':'model-proxy-browser-task'},
    data:{conversation_id:(await conversation.json()).id,mode:'task',input:{text:'执行测试任务并回复'}}
  });
  assert.equal(task.status(),201);
  const sessionId = (await task.json()).id;
  let completed = false;
  for (let i=0; i<60 && !completed; i++) {
    const events = await user.request.get(base+'/sessions/'+sessionId+'/events');
    assert.ok(events.ok());
    completed = (await events.text()).includes('Model proxy fixture reply');
    if (!completed) await new Promise(resolve=>setTimeout(resolve,250));
  }
  assert.ok(completed,'Task completed through the official model');
  await user.getByRole('button',{name:'刷新',exact:true}).click();
  await user.getByText('990.000000 积分',{exact:true}).waitFor();
  assert.equal(await user.getByText('已结算',{exact:true}).count(),2);

  await admin.getByRole('button',{name:'用户管理',exact:true}).click();
  await admin.getByRole('button',{name:'搜索',exact:true}).click();
  const account=admin.locator('article').filter({hasText:'user@browser.example'});
  await account.getByRole('button',{name:'赠送积分'}).click();
  await field(admin,'积分数量').fill('10');
  await field(admin,'赠送原因').fill('端到端测试赠送');
  await admin.getByRole('button',{name:'确认赠送'}).click();
  await account.getByText('1000.000000 积分',{exact:true}).waitFor();
  await user.getByRole('button',{name:'刷新',exact:true}).click();
  await user.getByText('1000.000000 积分',{exact:true}).waitFor();

  await user.setViewportSize({width:390,height:844});
  assert.ok(await user.evaluate(()=>document.documentElement.scrollWidth <= innerWidth));
  assert.deepEqual(exceptions,[]);
  console.log('Chrome: registration, official model management, employee creation, Pi chat and task, settlement and credit grant verified');
} finally {
  await browser.close();
}
