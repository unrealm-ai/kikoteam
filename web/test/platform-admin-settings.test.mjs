import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { Window } from 'happy-dom';
import { parse, compileScript } from '@vue/compiler-sfc';
import esbuild from 'esbuild';

const window = new Window({url: 'http://127.0.0.1:5173/'});
for (const name of ['window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame']) {
  if (name === 'window') globalThis.window = window;
  else if (window[name] !== undefined) Object.defineProperty(globalThis, name, {
    configurable:true, writable:true,
    value:typeof window[name] === 'function' && ['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(name) ? window[name].bind(window) : window[name],
  });
}
const built = await esbuild.build({
  stdin:{
    contents:`export {default as PlatformAdminView} from './src/direction2/views/PlatformAdminView.vue';
      export {createApp,h,nextTick} from 'vue'; export {NMessageProvider} from 'naive-ui';`,
    resolveDir:process.cwd(),loader:'ts',
  },
  bundle:true,write:false,format:'esm',platform:'node',
  define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},
  plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{
    const {descriptor}=parse(await fs.readFile(path,'utf8'));
    return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};
  });}}],
});
const {PlatformAdminView,createApp,h,nextTick,NMessageProvider} =
  await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const wait = async () => { await nextTick(); await new Promise(r => setTimeout(r, 35)); await nextTick(); };
const root = '/api/v1/platform';
const mail = root+'/mail-settings';
const github = root+'/oauth-apps/github';
const channelDownloads = root+'/channel-download-settings';
const referralSettings = root+'/referral-settings';
const baseMail = {
  version:4,source:'database',enabled:true,registration_verification_required:false,address:'smtp.example.test:587',
  host:'smtp.example.test',username:'notify@example.test',from:'notify@example.test',implicit_tls:false,password_configured:true,
};
const baseGithub = {
  provider:'github',purpose:'plugin',version:2,enabled:true,client_id:'existing-test-id',has_client_secret:true,
  source:'database',redirect_uri:'https://kiko.example.test/api/v1/plugins/oauth/github/callback',
};

async function mount(handle) {
  const requests = [], errors = [];
  globalThis.fetch = async (url, init) => {
    const request = {url,method:init.method||'GET',body:init.body ? JSON.parse(init.body) : undefined,headers:init.headers};
    requests.push(request);
    if (url.includes('/accounts?')) return Response.json({items:[],total:0});
    if (url.endsWith('/task-link-settings') && request.method === 'GET') return Response.json({version:0,origin:'',enabled:true});
    if (url.endsWith('/credit-settings')) return Response.json({signup_bonus:'0',version:1});
    if (url === referralSettings && request.method === 'GET') return Response.json({enabled:false,version:0});
    return handle(request);
  };
  const host = document.createElement('div'); document.body.appendChild(host);
  let admin;
  const app = createApp({render:()=>h(NMessageProvider,null,{default:()=>h(PlatformAdminView,{ref:vm=>{admin=vm;}})})});
  app.config.errorHandler = e => errors.push(String(e));
  app.mount(host);
  await wait();
  return {
    host,requests,errors,
    saveSection: () => admin.save(),
    button(label) {
      const button = [...host.querySelectorAll('button')].find(node => node.textContent.trim() === label);
      assert.ok(button, label);
      return button;
    },
    input(id) {
      const input = host.querySelector('#'+id);
      assert.ok(input, id);
      return input;
    },
    async edit(id, value) {
      const input = this.input(id);
      input.value = value; input.dispatchEvent(new window.Event('input',{bubbles:true}));
      await wait();
    },
    async submit(selector) {
      const form = host.querySelector(selector+' form');
      assert.ok(form, selector);
      form.dispatchEvent(new window.Event('submit',{bubbles:true,cancelable:true}));
      await wait();
    },
    async choose(label, selector) {
      const control = [...host.querySelectorAll(selector)].find(node => node.textContent.trim() === label);
      assert.ok(control, label);
      control.click(); await wait();
    },
    async close() { app.unmount(); host.remove(); await wait(); },
  };
}

test('image settings test the candidate, save its receipt, and change price without rereading the secret', async()=>{
  const path=root+'/default-plugins/image-generation';
  let config={name:'官方文生图',release_version:1,version:0,enabled:false,provider:'openai_images',base_url:'https://fixture.example/v1',upstream_model_id:'',credential_configured:false,credits_per_image:'5.000000',workspace_daily_image_limit:100,max_concurrent_generations:8,timeout_seconds:120};
  const m=await mount(r=>{
    if(r.url.includes('/image-generations?'))return Response.json({items:[],next_cursor:''});
    if(r.url===path+'/test')return Response.json({receipt:'tested',preview_png:'AA==',expires_at:new Date(Date.now()+600000).toISOString()});
    if(r.url===path&&r.method==='PATCH'){config={...config,provider:r.body.provider??config.provider,upstream_model_id:'fixture',credential_configured:true,version:config.version+1,credits_per_image:r.body.credits_per_image??config.credits_per_image};}
    if(r.url===path)return Response.json(config);
    throw Error('unexpected URL');
  });
  try{
    m.button('默认插件').click();await wait();await m.edit('image-model-id','fixture');await m.edit('image-api-key','test-secret');
    m.button('测试出图').click();await wait();assert.ok(m.host.querySelector('img[alt="供应商测试样图"]'));
    const inline=[...m.host.querySelectorAll('[role="checkbox"]')].find(node=>node.textContent.includes('Base64'));
    assert.ok(inline);inline.click();await wait();assert.equal(m.host.querySelector('img[alt="供应商测试样图"]'),null);
    m.button('测试出图').click();await wait();assert.equal(m.requests.filter(r=>r.url===path+'/test').at(-1).body.provider,'openai_images_b64_json');
    await m.submit('.image-settings');
    const first=m.requests.find(r=>r.method==='PATCH');assert.equal(first.body.receipt,'tested');assert.equal(first.body.version,0);assert.equal(first.body.provider,'openai_images_b64_json');
    assert.equal(m.input('image-api-key').value,'');
    await m.edit('image-unit-price','8.000000');await m.submit('.image-settings');
    const last=m.requests.filter(r=>r.method==='PATCH').at(-1);assert.equal(last.body.version,1);assert.equal(last.body.credits_per_image,'8.000000');assert.equal(last.body.api_key,undefined);
    assert.deepEqual(m.errors,[]);
  }finally{await m.close();}
});

test('SMTP test uses the unsaved draft without saving or revealing the stored password', async () => {
  const m = await mount(r => {
    if (r.url === mail) return Response.json(baseMail);
    if (r.url === mail+'/test') return Response.json({ok:true,test_receipt:'fixture-smtp-receipt'});
    throw new Error('Unexpected endpoint');
  });
  try {
    assert.ok(!m.requests.some(r => r.url === mail || r.url === mail+'/test'));
    m.button('注册与邀请').click(); await wait();
    assert.equal(m.input('platform-smtp-password').value, '');
    assert.equal(m.input('platform-smtp-password').type, 'password');
    await m.choose('注册时必须验证邮箱','.n-checkbox');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    const testCalls = m.requests.filter(r => r.url === mail+'/test');
    assert.equal(testCalls.length,1);
    assert.equal(testCalls[0].method,'POST');
    assert.equal(testCalls[0].headers.get('X-CSRF-Protection'),'1');
    assert.deepEqual(testCalls[0].body,{
      version:4,enabled:true,registration_verification_required:true,address:'smtp.example.test:587',
      host:'smtp.example.test',username:'notify@example.test',from:'notify@example.test',implicit_tls:false,
    });
    assert.ok(!m.requests.some(r => r.method === 'PATCH'));
    assert.match(m.host.textContent,/SMTP 服务器已接受.*测试邮件/);
    assert.equal(m.input('platform-smtp-from').value,'notify@example.test');
    await m.edit('platform-smtp-from','other@example.test');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    assert.equal(m.requests.filter(r => r.url === mail+'/test').length,1);
    assert.match(m.host.textContent,/不能沿用旧凭据/);
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('SMTP revision conflict rebases a harmless policy edit; retry stays explicit', async () => {
  let reads = 0, writes = 0;
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') {
      return Response.json({...baseMail,...(++reads > 1 ? {registration_verification_required:true,version:5} : {})});
    }
    if (r.url === mail+'/test') return Response.json({ok:true,test_receipt:'fixture-smtp-receipt-'+r.body.version});
    if (r.url === mail && r.method === 'PATCH') {
      if (++writes === 1) return Response.json({error:{code:'mail_settings_version_conflict',message:'conflict'}},{status:409});
      return Response.json({...baseMail,registration_verification_required:r.body.registration_verification_required,version:6});
    }
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('注册与邀请').click(); await wait();
    await m.edit('platform-smtp-password','fixture-replacement-password');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    await m.submit('[aria-label="邮件与注册设置"]');
    assert.match(m.host.textContent,/配置已被其他管理员更新/);
    assert.equal(m.input('platform-smtp-password').value,'fixture-replacement-password');
    assert.ok(m.button('保存邮件设置').disabled);
    m.button('读取最新版本').click(); await wait();
    assert.equal(writes,1);
    assert.equal(m.input('platform-smtp-from').value,'notify@example.test');
    assert.equal(m.input('platform-smtp-password').value,'fixture-replacement-password');
    await m.submit('[aria-label="邮件与注册设置"]');
    assert.ok(m.button('保存邮件设置').disabled);
    assert.equal(writes,1);
    m.button('发测试邮件到我的邮箱').click(); await wait();
    await m.submit('[aria-label="邮件与注册设置"]');
    const submissions=m.requests.filter(r=>r.method==='PATCH');
    assert.equal(submissions[1].body.test_receipt,'fixture-smtp-receipt-5');
    assert.equal(submissions[1].body.version,5);
    assert.equal(submissions[1].body.registration_verification_required,true);
    assert.equal(submissions[1].body.from,'notify@example.test');
    assert.equal(submissions[1].body.password,'fixture-replacement-password');
    assert.equal(m.input('platform-smtp-password').value,'');
    assert.match(m.host.textContent,/已保存并生效/);
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('GitHub plugin screen preserves a blank secret, never claims website login, and clears only explicitly', async () => {
  let status = {...baseGithub};
  const m = await mount(r => {
    if (r.url === github && r.method === 'GET') return Response.json(status);
    if (r.url === github && r.method === 'PATCH') {
      status = {...status,version:status.version+1,enabled:r.body.enabled,client_id:r.body.client_id ?? status.client_id,has_client_secret:!r.body.clear_client_secret};
      return Response.json(status);
    }
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('插件集成').click(); await wait();
    assert.match(m.host.textContent,/不用于登录 KIKO 网站/);
    assert.equal(m.input('platform-github-oauth-redirect').value,baseGithub.redirect_uri);
    assert.equal(m.input('platform-github-oauth-secret').value,'');
    assert.ok(!m.requests.some(r => r.url === mail));
    const writes=()=>m.requests.filter(r=>r.method==='PATCH');
    const check = label => [...m.host.querySelectorAll('.n-checkbox')].find(n => n.textContent.includes(label));
    check('启用 GitHub 插件 OAuth').click(); await wait();
    await m.submit('[aria-label="集成设置"]');
    assert.deepEqual(writes()[0].body,{expected_version:2,enabled:false});
    check('启用 GitHub 插件 OAuth').click(); await wait();
    await m.edit('platform-github-oauth-id','different-test-id');
    await m.submit('[aria-label="集成设置"]');
    assert.equal(writes().length,1);
    assert.match(m.host.textContent,/更换 Client ID 时必须输入/);
    await m.edit('platform-github-oauth-secret','fixture-new-secret');
    await m.submit('[aria-label="集成设置"]');
    assert.deepEqual(writes()[1].body,{expected_version:3,enabled:true,client_id:'different-test-id',client_secret:'fixture-new-secret'});
    check('启用 GitHub 插件 OAuth').click(); await wait();
    check('保存时清除原 Client Secret').click(); await wait();
    await m.submit('[aria-label="集成设置"]');
    assert.deepEqual(writes()[2].body,{expected_version:4,enabled:false,clear_client_secret:true});
    assert.equal(m.input('platform-github-oauth-secret').value,'');
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('Channel attachment IP validation starts enabled and applies changes only after saving', async () => {
  let settings = {version:0, validate_public_ip:true};
  const m = await mount(r => {
    if (r.url === channelDownloads && r.method === 'GET') return Response.json(settings);
    if (r.url === channelDownloads && r.method === 'PATCH') {
      settings = {version:settings.version+1, validate_public_ip:r.body.validate_public_ip};
      return Response.json(settings);
    }
    throw new Error('Unexpected endpoint');
  });
  try {
    assert.ok(!m.requests.some(r => r.url === channelDownloads));
    m.button('任务分享与附件').click(); await wait();
    const checkbox = () => m.host.querySelector('[aria-label="附件下载设置"] [role="checkbox"]');
    assert.equal(checkbox()?.getAttribute('aria-checked'), 'true');
    assert.ok(m.button('保存附件下载设置').disabled);
    checkbox().click(); await wait();
    assert.equal(checkbox()?.getAttribute('aria-checked'), 'false');
    assert.ok(!m.requests.some(r => r.method === 'PATCH'));
    assert.equal(await m.saveSection(), true); await wait();
    assert.deepEqual(m.requests.filter(r => r.method === 'PATCH').map(r => r.body), [
      {expected_version:0, validate_public_ip:false},
    ]);
    assert.ok(m.button('保存附件下载设置').disabled);
    assert.match(m.host.textContent, /已保存并生效/);
    assert.deepEqual(m.errors, []);
  } finally { await m.close(); }
});

test('SMTP draft passwords do not follow concurrent connection targets after rebasing', async () => {
  let reads=0;
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') return Response.json({...baseMail,...(++reads>1 ? {
      version:5,address:'different.example.test:587',host:'different.example.test',
    } : {})});
    if (r.url === mail && r.method === 'PATCH') return Response.json({error:{code:'mail_settings_version_conflict'}},{status:409});
    if (r.url === mail+'/test') return Response.json({ok:true,test_receipt:'fixture-smtp-receipt'});
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('注册与邀请').click(); await wait();
    await m.edit('platform-smtp-password','fixture-replacement-password');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    await m.submit('[aria-label="邮件与注册设置"]');
    m.button('读取最新版本').click(); await wait();
    assert.equal(m.input('platform-smtp-address').value,'different.example.test:587');
    assert.equal(m.input('platform-smtp-from').value,'notify@example.test');
    assert.equal(m.input('platform-smtp-password').value,'');
    assert.match(m.host.textContent,/SMTP 连接目标已变化/);
    assert.equal(m.requests.filter(r=>r.method==='PATCH').length,1);
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('SMTP admin password lockout rejection is not reported as a version conflict', async () => {
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') return Response.json(baseMail);
    if (r.url === mail && r.method === 'PATCH') return Response.json({error:{code:'platform_admin_password_required'}},{status:409});
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('注册与邀请').click(); await wait();
    const enabled = [...m.host.querySelectorAll('.n-checkbox')].find(n=>n.textContent.includes('启用邮件'));
    enabled.click(); await wait();
    await m.submit('[aria-label="邮件与注册设置"]');
    assert.equal(m.requests.filter(r=>r.method==='PATCH').length,1);
    assert.match(m.host.textContent,/至少需要一位可用密码登录的平台管理员/);
    assert.doesNotMatch(m.host.textContent,/配置已被其他管理员更新/);
    assert.equal(m.host.querySelector('#platform-smtp-password'),null);
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('new 163 preset accepts only email and code; testing sends the expanded TLS profile without saving', async () => {
  const empty = {...baseMail,enabled:false,source:'unset',version:0,address:'',host:'',from:'',username:'',password_configured:false};
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') return Response.json(empty);
    if (r.url === mail+'/test') return Response.json({ok:true,test_receipt:'fixture-smtp-receipt'});
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('注册与邀请').click(); await wait();
    assert.match(m.host.textContent,/尚未配置/);
    assert.equal(m.host.querySelector('#platform-smtp-address'),null);
    assert.equal(m.host.querySelector('#platform-smtp-host'),null);
    assert.equal(m.host.querySelector('#platform-smtp-username'),null);
    await m.edit('platform-smtp-from',' notify@163.com ');
    await m.edit('platform-smtp-password','fixture-client-code');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    assert.deepEqual(m.requests.find(r=>r.url === mail+'/test').body,{
      version:0,enabled:true,registration_verification_required:true,
      address:'smtp.163.com:465',host:'smtp.163.com',username:'notify@163.com',from:'notify@163.com',
      implicit_tls:true,password:'fixture-client-code',
    });
    assert.ok(!m.requests.some(r=>r.method==='PATCH'));
    await m.choose('自定义 SMTP','.n-radio');
    assert.equal(m.input('platform-smtp-password').value,'');
    assert.equal(m.input('platform-smtp-address').value,'');
    for (const bad of ['192.0.2.1:465','smtp.example.test:25','https://smtp.example.test:465']) {
      await m.edit('platform-smtp-address',bad);
      m.button('发测试邮件到我的邮箱').click(); await wait();
    }
    assert.equal(m.requests.filter(r=>r.url === mail+'/test').length,1);
    assert.match(m.host.textContent,/只支持 465 或 587/);
    assert.match(m.host.textContent,/SMTP 密码或授权码/);
    await m.edit('platform-smtp-from','mailer@example.test');
    await m.edit('platform-smtp-address','smtp.internal.example.test:587');
    await m.edit('platform-smtp-password','fixture-custom-code');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    const custom=m.requests.filter(r=>r.url===mail+'/test').at(-1).body;
    assert.equal(custom.address,'smtp.internal.example.test:587');
    assert.equal(custom.host,'smtp.internal.example.test');
    assert.equal(custom.implicit_tls,false);
    assert.equal(custom.username,'mailer@example.test');
    assert.equal(custom.username,custom.from);
    await m.edit('platform-smtp-address','smtp.internal.example.test:465');
    assert.equal(m.input('platform-smtp-password').value,'');
    await m.edit('platform-smtp-password','fixture-tls-code');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    const implicit=m.requests.filter(r=>r.url===mail+'/test').at(-1).body;
    assert.equal(implicit.address,'smtp.internal.example.test:465');
    assert.equal(implicit.implicit_tls,true);
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('incompatible saved SMTP is not edited or tested implicitly and can be disabled without rewriting its endpoint', async () => {
  const legacy = {...baseMail,address:'192.0.2.1:2525',host:'alias.example.test',username:'login',from:'notify@example.test',registration_verification_required:true};
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') return Response.json(legacy);
    if (r.url === mail && r.method === 'PATCH') return Response.json({...legacy,enabled:false,registration_verification_required:false,version:5});
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('注册与邀请').click(); await wait();
    assert.match(m.host.textContent,/现有 SMTP 配置不符合简化表单/);
    assert.equal(m.host.querySelector('#platform-smtp-password'),null);
    assert.equal(m.button('发测试邮件到我的邮箱').disabled,true);
    await m.choose('自定义 SMTP','.n-radio');
    assert.equal(m.input('platform-smtp-from').value,'');
    assert.equal(m.input('platform-smtp-address').value,'');
    await m.edit('platform-smtp-from','replacement@example.test');
    await m.edit('platform-smtp-address','smtp.example.test:465');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    assert.match(m.host.textContent,/不能沿用旧凭据/);
    assert.ok(!m.requests.some(r=>r.url===mail+'/test'));
    await m.choose('启用邮件','.n-checkbox');
    await m.submit('[aria-label="邮件与注册设置"]');
    assert.deepEqual(m.requests.find(r=>r.method==='PATCH').body,{
      version:4,enabled:false,registration_verification_required:false,address:legacy.address,host:legacy.host,
      username:legacy.username,from:legacy.from,implicit_tls:false,
    });
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('SMTP preset replacement is atomic across a concurrent incompatible endpoint change', async () => {
  const saved={...baseMail,address:'smtp.163.com:465',host:'smtp.163.com',from:'first@163.com',username:'first@163.com',implicit_tls:true};
  let reads=0;
  const m=await mount(r=>{
    if(r.url===mail&&r.method==='GET') return Response.json(++reads===1?saved:{...saved,version:5,host:'concurrent-alias.example.test'});
    if(r.url===mail&&r.method==='PATCH') return Response.json({error:{code:'mail_settings_version_conflict'}},{status:409});
    if(r.url===mail+'/test') return Response.json({ok:true,test_receipt:'fixture-smtp-receipt'});
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('注册与邀请').click(); await wait();
    assert.equal(m.host.querySelector('#platform-smtp-address'),null);
    assert.equal(m.input('platform-smtp-password').value,'');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    assert.equal(m.requests.find(r=>r.url===mail+'/test').body.password,undefined);
    await m.edit('platform-smtp-from','second@163.com');
    await m.edit('platform-smtp-password','fixture-replacement-code');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    await m.submit('[aria-label="邮件与注册设置"]');
    m.button('读取最新版本').click(); await wait();
    assert.equal(m.requests.filter(r=>r.method==='PATCH').length,1);
    assert.equal(m.input('platform-smtp-from').value,'second@163.com');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    const tested=m.requests.filter(r=>r.url===mail+'/test').at(-1).body;
    assert.equal(tested.version,5);
    assert.equal(tested.host,'smtp.163.com');
    assert.equal(tested.username,'second@163.com');
    assert.equal(tested.password,'fixture-replacement-code');
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('SMTP receipt is required by form save, survives a policy edit, and is invalidated by a credential change or backend rejection', async () => {
  let attempts = 0;
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') return Response.json(baseMail);
    if (r.url === mail+'/test') return Response.json({ok:true,test_receipt:'fixture-proof'});
    if (r.url === mail && r.method === 'PATCH') {
      if (++attempts === 1) return Response.json({error:{code:'smtp_test_required'}},{status:400});
      return Response.json({...baseMail,version:5,registration_verification_required:r.body.registration_verification_required});
    }
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('注册与邀请').click(); await wait();
    await m.choose('注册时必须验证邮箱','.n-checkbox');
    assert.equal(m.button('保存邮件设置').disabled,true);
    assert.equal(await m.saveSection(),false); await wait();
    await m.submit('[aria-label="邮件与注册设置"]');
    assert.equal(attempts,0);
    m.button('发测试邮件到我的邮箱').click(); await wait();
    assert.equal(m.button('保存邮件设置').disabled,false);
    await m.choose('注册时必须验证邮箱','.n-checkbox');
    await m.choose('注册时必须验证邮箱','.n-checkbox');
    assert.equal(m.button('保存邮件设置').disabled,false);
    await m.edit('platform-smtp-password','new-fixture-secret');
    await m.edit('platform-smtp-password','');
    assert.equal(m.button('保存邮件设置').disabled,true);
    m.button('发测试邮件到我的邮箱').click(); await wait();
    await m.submit('[aria-label="邮件与注册设置"]');
    assert.equal(attempts,1);
    assert.match(m.host.textContent,/SMTP 测试已失效/);
    assert.equal(m.button('保存邮件设置').disabled,true);
    m.button('发测试邮件到我的邮箱').click(); await wait();
    assert.equal(await m.saveSection(),true); await wait();
    assert.equal(attempts,2);
    assert.deepEqual(m.requests.filter(r=>r.method==='PATCH').map(r=>r.body.test_receipt),['fixture-proof','fixture-proof']);
    assert.ok(m.requests.filter(r=>r.url===mail+'/test').every(r=>r.body.test_receipt===undefined));
    assert.equal(m.button('保存邮件设置').disabled,true);
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('an explicitly disabled database record with no SMTP profile stays disabled', async () => {
  const empty = {...baseMail,enabled:false,address:'',host:'',from:'',username:'',password_configured:false};
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') return Response.json(empty);
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('注册与邀请').click(); await wait();
    assert.equal(m.host.querySelector('#platform-smtp-from'),null);
    assert.match(m.host.textContent,/平台已保存/);
    assert.equal(m.button('保存邮件设置').disabled,true);
    await m.submit('[aria-label="邮件与注册设置"]');
    assert.ok(!m.requests.some(r=>r.method==='PATCH'));
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('enabling SMTP defaults to registration verification and allows opting out before saving', async () => {
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') return Response.json({...baseMail,enabled:false});
    if (r.url === mail+'/test') return Response.json({ok:true,test_receipt:'fixture-proof'});
    if (r.url === mail && r.method === 'PATCH') return Response.json({...baseMail,...r.body,version:5});
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('注册与邀请').click(); await wait();
    const verification = () => [...m.host.querySelectorAll('.n-checkbox')]
      .find(node => node.textContent.trim() === '注册时必须验证邮箱').getAttribute('aria-checked');
    assert.equal(verification(),'false');
    await m.choose('启用邮件','.n-checkbox');
    assert.equal(verification(),'true');
    await m.choose('启用邮件','.n-checkbox');
    assert.equal(verification(),'false');
    await m.choose('启用邮件','.n-checkbox');
    assert.equal(verification(),'true');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    assert.equal(m.requests.find(r=>r.url===mail+'/test').body.registration_verification_required,true);
    assert.ok(!m.requests.some(r=>r.method==='PATCH'));
    await m.choose('注册时必须验证邮箱','.n-checkbox');
    assert.equal(await m.saveSection(),true);
    const saved = m.requests.find(r=>r.url===mail && r.method==='PATCH').body;
    assert.equal(saved.enabled,true);
    assert.equal(saved.registration_verification_required,false);
    assert.equal(verification(),'false');
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('SMTP receipt expiry blocks saves again without resetting the form', async () => {
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') return Response.json(baseMail);
    if (r.url === mail+'/test') return Response.json({ok:true,test_receipt:'fixture-proof'});
    throw new Error('Unexpected endpoint');
  });
  const originalTimeout = globalThis.setTimeout;
  let expire;
  globalThis.setTimeout = (callback, milliseconds, ...args) => {
    if (milliseconds === 600_000) {
      expire = callback;
      return undefined;
    }
    return originalTimeout(callback, milliseconds, ...args);
  };
  try {
    m.button('注册与邀请').click(); await wait();
    await m.choose('注册时必须验证邮箱','.n-checkbox');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    assert.equal(m.button('保存邮件设置').disabled,false);
    assert.equal(typeof expire,'function');
    expire(); await wait();
    assert.equal(m.button('保存邮件设置').disabled,true);
    assert.equal(await m.saveSection(),false); await wait();
    assert.equal(m.input('platform-smtp-from').value,baseMail.from);
    assert.ok(!m.requests.some(r=>r.method==='PATCH'));
    assert.deepEqual(m.errors,[]);
  } finally { globalThis.setTimeout = originalTimeout; await m.close(); }
});

test('Referral rewards are off by default, save an explicit switch, and notify the workbench', async () => {
  let changed = 0;
  const listener = () => { changed++; };
  window.addEventListener('kiko:referral-settings-changed', listener);
  const m = await mount(r => {
    if (r.url === referralSettings && r.method === 'PATCH') return Response.json({enabled:true,version:1});
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('注册与邀请').click(); await wait();
    assert.ok(m.button('保存邀请设置').disabled);
    await m.choose('开启奖励','.n-checkbox');
    m.button('保存邀请设置').click(); await wait();
    const write = m.requests.find(r => r.url === referralSettings && r.method === 'PATCH');
    assert.deepEqual(write?.body, {expected_version:0,enabled:true});
    assert.equal(changed, 1);
    assert.match(m.host.textContent, /邀请奖励设置已保存/);
    assert.deepEqual(m.errors,[]);
  } finally {
    window.removeEventListener('kiko:referral-settings-changed', listener);
    await m.close();
  }
});

test('The assistant provisioning switch is shown and saved from database settings alone', async () => {
  const assistant = root+'/assistant-settings';
  const m = await mount(r => {
    if (r.url.startsWith('/api/v1/platform-models?')) return Response.json({items:[],total:0});
    if (r.url === assistant && r.method === 'GET') return Response.json({version:1,enabled:false});
    if (r.url === assistant && r.method === 'PATCH') return Response.json({version:2,enabled:true});
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('官方模型与助手').click(); await wait();
    const control = () => m.host.querySelector('.assistant-settings [role="checkbox"]');
    assert.equal(control()?.getAttribute('aria-checked'), 'false');
    assert.doesNotMatch(m.host.textContent, /当前部署未启用/);
    assert.match(m.host.textContent, /还没有默认模型/);
    control().click(); await wait();
    assert.equal(await m.saveSection(), true);
    assert.deepEqual(m.requests.filter(r=>r.url===assistant && r.method==='PATCH').map(r=>r.body),[
      {expected_version:1,enabled:true},
    ]);
    assert.deepEqual(m.errors, []);
  } finally { await m.close(); }
});

test('Grouped settings still use independent APIs and find a default model outside the visible page', async () => {
  const assistant = root+'/assistant-settings';
  const olderModel = {
    id:'fixture-older', name:'旧默认模型', protocol:'chat_completions', base_url:'https://model.example.test',
    upstream_model_id:'fixture', has_key:false, prices:{input:'0',output:'0'}, enabled:true, validated:true, is_default:true, version:1,
  };
  const m = await mount(r => {
    if (r.url === mail) return Response.json(baseMail);
    if (r.url === github) return Response.json(baseGithub);
    if (r.url === channelDownloads) return Response.json({version:0,validate_public_ip:true});
    if (r.url === assistant) return Response.json({version:1,enabled:false});
    if (r.url === '/api/v1/platform-models?page=1') {
      return Response.json({items:[{...olderModel,id:'newer',name:'新模型',is_default:false}],total:101});
    }
    if (r.url === '/api/v1/platform-models?page=1&page_size=100') {
      return Response.json({items:[{...olderModel,id:'newer',name:'新模型',is_default:false}],total:101});
    }
    if (r.url === '/api/v1/platform-models?page=2&page_size=100') {
      return Response.json({items:[olderModel],total:101});
    }
    throw new Error('Unexpected endpoint: '+r.url);
  });
  try {
    const labels = [...m.host.querySelectorAll('.admin-subnav button')].map(n=>n.textContent.trim());
    assert.deepEqual(labels,['账户与注册','官方模型与助手','任务分享与附件','插件集成','默认插件']);
    m.button('官方模型与助手').click(); await wait();
    assert.match(m.host.querySelector('.assistant-settings').textContent, /旧默认模型（已验证、已启用）/);
    m.button('任务分享与附件').click(); await wait();
    assert.ok(m.host.querySelector('[aria-label="附件下载设置"]'));
    assert.ok(m.requests.some(r=>r.url.endsWith('/task-link-settings')));
    assert.ok(m.requests.some(r=>r.url===channelDownloads));
    m.button('插件集成').click(); await wait();
    assert.ok(m.requests.some(r=>r.url===github));
    assert.ok(!m.requests.some(r=>r.method==='PATCH'));
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});
