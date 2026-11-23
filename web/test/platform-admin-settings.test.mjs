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
    if (url.endsWith('/credit-settings')) return Response.json({signup_bonus:'0',version:1});
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

test('SMTP test uses the unsaved draft without saving or revealing the stored password', async () => {
  const m = await mount(r => {
    if (r.url === mail) return Response.json(baseMail);
    if (r.url === mail+'/test') return Response.json({ok:true,test_receipt:'fixture-smtp-receipt'});
    throw new Error('Unexpected endpoint');
  });
  try {
    assert.ok(!m.requests.some(r => r.url.startsWith(root)));
    m.button('邮件与注册').click(); await wait();
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
    m.button('邮件与注册').click(); await wait();
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
    m.button('集成设置').click(); await wait();
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
    m.button('附件下载').click(); await wait();
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

test('Legacy deployment settings are imported without changing fields or resending existing credentials', async () => {
  let smtpStatus = {...baseMail,source:'environment',version:0};
  let githubStatus = {...baseGithub,source:'legacy_env',version:0};
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') return Response.json(smtpStatus);
    if (r.url === github && r.method === 'GET') return Response.json(githubStatus);
    if (r.url === mail+'/test') return Response.json({ok:true,test_receipt:'fixture-smtp-receipt'});
    if (r.url === mail && r.method === 'PATCH') {
      smtpStatus = {...baseMail,version:1};
      return Response.json(smtpStatus);
    }
    if (r.url === github && r.method === 'PATCH') {
      githubStatus = {...baseGithub,version:1};
      return Response.json(githubStatus);
    }
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('邮件与注册').click(); await wait();
    assert.equal(m.input('platform-smtp-password').value,'');
    assert.ok(m.button('迁入邮件设置').disabled);
    m.button('发测试邮件到我的邮箱').click(); await wait();
    m.button('迁入邮件设置').click(); await wait();
    m.button('集成设置').click(); await wait();
    assert.equal(m.input('platform-github-oauth-secret').value,'');
    m.button('迁入 GitHub OAuth 设置').click(); await wait();
    assert.deepEqual(m.requests.filter(r=>r.method==='PATCH').map(r=>[r.url,r.body]),[
      [mail,{version:0,enabled:true,registration_verification_required:false,address:baseMail.address,host:baseMail.host,username:baseMail.username,from:baseMail.from,implicit_tls:false,test_receipt:'fixture-smtp-receipt'}],
      [github,{expected_version:0,enabled:true}],
    ]);
    assert.deepEqual(m.errors,[]);
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
    m.button('邮件与注册').click(); await wait();
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
    m.button('邮件与注册').click(); await wait();
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
  const empty = {...baseMail,enabled:false,source:'environment',version:0,address:'',host:'',from:'',username:'',password_configured:false};
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') return Response.json(empty);
    if (r.url === mail+'/test') return Response.json({ok:true,test_receipt:'fixture-smtp-receipt'});
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('邮件与注册').click(); await wait();
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
    m.button('邮件与注册').click(); await wait();
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
    m.button('邮件与注册').click(); await wait();
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
    m.button('邮件与注册').click(); await wait();
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
    m.button('邮件与注册').click(); await wait();
    assert.equal(m.host.querySelector('#platform-smtp-from'),null);
    assert.match(m.host.textContent,/平台已保存/);
    assert.equal(m.button('保存邮件设置').disabled,true);
    await m.submit('[aria-label="邮件与注册设置"]');
    assert.ok(!m.requests.some(r=>r.method==='PATCH'));
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
    m.button('邮件与注册').click(); await wait();
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
