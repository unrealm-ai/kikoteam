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
const baseMail = {
  version:4,source:'database',enabled:true,registration_verification_required:false,address:'smtp.example.test:587',
  host:'smtp.example.test',username:'test-smtp',from:'notify@example.test',implicit_tls:false,password_configured:true,
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
  const app = createApp({render:()=>h(NMessageProvider,null,{default:()=>h(PlatformAdminView)})});
  app.config.errorHandler = e => errors.push(String(e));
  app.mount(host);
  await wait();
  return {
    host,requests,errors,
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
    async close() { app.unmount(); host.remove(); await wait(); },
  };
}

test('SMTP test uses the unsaved draft without saving or revealing the stored password', async () => {
  const m = await mount(r => {
    if (r.url === mail) return Response.json(baseMail);
    if (r.url === mail+'/test') return Response.json({ok:true});
    throw new Error('Unexpected endpoint');
  });
  try {
    assert.ok(!m.requests.some(r => r.url.startsWith(root)));
    m.button('邮件与注册').click(); await wait();
    assert.equal(m.input('platform-smtp-password').value, '');
    assert.equal(m.input('platform-smtp-password').type, 'password');
    await m.edit('platform-smtp-from','new-notify@example.test');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    const testCalls = m.requests.filter(r => r.url === mail+'/test');
    assert.equal(testCalls.length,1);
    assert.equal(testCalls[0].method,'POST');
    assert.equal(testCalls[0].headers.get('X-CSRF-Protection'),'1');
    assert.deepEqual(testCalls[0].body,{
      version:4,enabled:true,registration_verification_required:false,address:'smtp.example.test:587',
      host:'smtp.example.test',username:'test-smtp',from:'new-notify@example.test',implicit_tls:false,
    });
    assert.ok(!m.requests.some(r => r.method === 'PATCH'));
    assert.match(m.host.textContent,/SMTP 服务器已接受.*测试邮件/);
    assert.equal(m.input('platform-smtp-from').value,'new-notify@example.test');
    await m.edit('platform-smtp-address','other.example.test:587');
    m.button('发测试邮件到我的邮箱').click(); await wait();
    assert.equal(m.requests.filter(r => r.url === mail+'/test').length,1);
    assert.match(m.host.textContent,/不能沿用旧密码/);
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});

test('SMTP revision conflict rebases only touched fields; retry stays explicit', async () => {
  let reads = 0, writes = 0;
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') {
      return Response.json({...baseMail,...(++reads > 1 ? {from:'concurrent@example.test',version:5} : {})});
    }
    if (r.url === mail && r.method === 'PATCH') {
      if (++writes === 1) return Response.json({error:{code:'mail_settings_version_conflict',message:'conflict'}},{status:409});
      return Response.json({...baseMail,from:r.body.from,version:6});
    }
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('邮件与注册').click(); await wait();
    await m.edit('platform-smtp-password','fixture-replacement-password');
    await m.submit('[aria-label="邮件与注册设置"]');
    assert.match(m.host.textContent,/配置已被其他管理员更新/);
    assert.equal(m.input('platform-smtp-password').value,'fixture-replacement-password');
    assert.ok(m.button('保存邮件设置').disabled);
    m.button('读取最新版本').click(); await wait();
    assert.equal(writes,1);
    assert.equal(m.input('platform-smtp-from').value,'concurrent@example.test');
    assert.equal(m.input('platform-smtp-password').value,'fixture-replacement-password');
    await m.submit('[aria-label="邮件与注册设置"]');
    const submissions=m.requests.filter(r=>r.method==='PATCH');
    assert.equal(submissions[1].body.version,5);
    assert.equal(submissions[1].body.from,'concurrent@example.test');
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

test('Legacy deployment settings are imported without changing fields or resending existing credentials', async () => {
  let smtpStatus = {...baseMail,source:'environment',version:0};
  let githubStatus = {...baseGithub,source:'legacy_env',version:0};
  const m = await mount(r => {
    if (r.url === mail && r.method === 'GET') return Response.json(smtpStatus);
    if (r.url === github && r.method === 'GET') return Response.json(githubStatus);
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
    m.button('迁入邮件设置').click(); await wait();
    m.button('集成设置').click(); await wait();
    assert.equal(m.input('platform-github-oauth-secret').value,'');
    m.button('迁入 GitHub OAuth 设置').click(); await wait();
    assert.deepEqual(m.requests.filter(r=>r.method==='PATCH').map(r=>[r.url,r.body]),[
      [mail,{version:0,enabled:true,registration_verification_required:false,address:baseMail.address,host:baseMail.host,username:baseMail.username,from:baseMail.from,implicit_tls:false}],
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
    throw new Error('Unexpected endpoint');
  });
  try {
    m.button('邮件与注册').click(); await wait();
    await m.edit('platform-smtp-from','new-notify@example.test');
    await m.edit('platform-smtp-password','fixture-replacement-password');
    await m.submit('[aria-label="邮件与注册设置"]');
    m.button('读取最新版本').click(); await wait();
    assert.equal(m.input('platform-smtp-address').value,'different.example.test:587');
    assert.equal(m.input('platform-smtp-from').value,'new-notify@example.test');
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
    assert.equal(m.input('platform-smtp-password').value,'');
    assert.deepEqual(m.errors,[]);
  } finally { await m.close(); }
});
