import {channelsApi, type ChannelProvider} from './channels';
import {channelCredentialPayload, workforceCredentialTest, type WorkforceChannelCredentials} from './workforce-channel-credentials';
import type {DigitalEmployee} from '../types';
import type {FeishuBinding} from './feishu';
import {feishuSetupApi} from './feishu-setup';

export function createWorkforceChannelActions(options:{
 workspace:()=>string;
 currentBinding:(w:string,a:string,p:ChannelProvider)=>FeishuBinding|undefined;
 reloadEmployee:(w:string,a:string)=>Promise<DigitalEmployee>;
}) {
 const signal=()=>AbortSignal.timeout(75000);
 type Payload=WorkforceChannelCredentials&{channelTestReceipt:string;confirmedImpact?:boolean};
 const testReceipts=new Map<string,{version:number;signature:string}>();
 const scope=(w:string,a:string,p:ChannelProvider)=>JSON.stringify([w,a,p]);
 async function save(employee:string,provider:ChannelProvider,payload:Payload){
  const w=options.workspace();const current=options.currentBinding(w,employee,provider);
  const credentials=channelCredentialPayload(provider,payload);
  const test=testReceipts.get(scope(w,employee,provider));
  if(!test||test.signature!==JSON.stringify(credentials))throw new Error('凭据已修改，请重新检查后再保存');
  const saved=await channelsApi.save(w,employee,provider,{...credentials,version:test.version,enabled:current?.enabled??true,
    ...(payload.channelTestReceipt?{test_receipt:payload.channelTestReceipt}:{}),...(payload.confirmedImpact?{confirmed_impact:true}:{})},signal());
  testReceipts.delete(scope(w,employee,provider));
  const result=await options.reloadEmployee(w,employee);
  const channel=result.channels.find(item=>item.platform===provider);
  if(!channel)throw new Error('凭据已保存，但读取渠道状态失败，请刷新后查看，勿重复提交');
  return {employee:result,channel,switchingRobot:!!current&&current.app_id!==saved.app_id};
 }
 return {
  getChannelRebindImpact: channelsApi.rebindImpact,
  async testChannelConnection(employee:string,provider:ChannelProvider,payload:WorkforceChannelCredentials&{useStored?:boolean;configVersion?:number}){
    if(payload.useStored){
      const w=options.workspace();const started=Date.now();
      const diagnostics=provider==='feishu'?await feishuSetupApi.diagnose(w,employee,signal()):undefined;
      const fresh=await options.reloadEmployee(w,employee);
      const channel=fresh.channels.find(item=>item.platform===provider);
      return {ok:!!channel&&channel.connectionState==='connected',platform:provider,latency:`${Date.now()-started}ms`,channelTestReceipt:'',testedAt:diagnostics?.checked_at||''};
    }
    const w=options.workspace();const version=payload.configVersion??options.currentBinding(w,employee,provider)?.config_revision??0;
    const started=Date.now();const result=await workforceCredentialTest(w,employee,provider,version,payload,signal());
    testReceipts.set(scope(w,employee,provider),{version,signature:JSON.stringify(channelCredentialPayload(provider,payload))});
    return {ok:true,platform:provider,latency:`${Date.now()-started}ms`,channelTestReceipt:result.receipt||'',testedAt:result.tested_at,credentialStatus:result.credential_status};
  },
  bindChannelCredentials:save,rebindChannelCredentials:save,
  async toggleEmployeeChannelStatus(employee:string,provider:ChannelProvider,enabled:boolean){
    const w=options.workspace();const current=options.currentBinding(w,employee,provider);
    if(!current)throw new Error('请先读取渠道状态');
    await channelsApi.save(w,employee,provider,{version:current.config_revision,app_id:current.app_id,enabled},signal());
    return options.reloadEmployee(w,employee);
  }
 };
}
