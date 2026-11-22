import { buildCanonicalPrincipalKey, type PrincipalRef, type ChannelDirectoryUser, type ChannelDirectoryDepartment, type ConnectionDirectoryQueryResult } from '../types';
import { selectorsApi, toUsageSelector, selectorKey, type SelectionCandidate, type SelectorPurpose } from './selectors';
import type { UsageChannel, UsageSelector } from './agent-usage';
export interface WorkforceDirectoryContext {workspace: string; employeeId: string; purpose?: SelectorPurpose}
export type DirectoryPrincipal = PrincipalRef & {usageSelector: UsageSelector; selectionCandidate: SelectionCandidate};
export function directoryPrincipal(row: SelectionCandidate): DirectoryPrincipal {
  if (row.ref.kind === 'group') throw new Error('通讯录只支持人员和部门');
  const value = {channel:row.ref.provider,connectionId:row.ref.binding_id || '',targetType:row.ref.kind,targetId:row.ref.external_id,displayName:row.display_name || row.name || row.ref.external_id};
  return {...value,canonicalKey:buildCanonicalPrincipalKey(value),usageSelector:toUsageSelector(row.ref, false),selectionCandidate:structuredClone(row)};
}
export function directoryUser(row: SelectionCandidate): ChannelDirectoryUser {
  return {id:row.search_user_id || row.ref.external_id,refId:row.ref.external_id,name:row.display_name || row.name || row.ref.external_id,department:(row.department_paths||[]).join('；'),email:'',platform:row.ref.provider,sourceType:row.ref.provider,connectionId:row.ref.binding_id || '',avatar:'',botAuthorized:row.selectable,isWecomVerified:row.verification_status==='verified',bindingStatus:row.selectable?'verified':'pending_binding',principal:directoryPrincipal(row)};
}
export function directoryDepartment(row: SelectionCandidate): ChannelDirectoryDepartment {
  return {id:row.ref.external_id,refId:row.ref.external_id,name:row.display_name || row.name || row.ref.external_id,platform:row.ref.provider,sourceType:row.ref.provider,connectionId:row.ref.binding_id || '',memberCount:0,principal:directoryPrincipal(row)};
}
/** Cursor-backed paging retains original Previous/Next UI without inventing totals. */
export function createWorkforceDirectoryReader(api = selectorsApi) {
  type Entry = {people: (string|null)[]; departments:(string|null)[]; snapshot?:number};
  const pages = new Map<string,Entry>();
  return {
    reset() { pages.clear(); },
    async page(context:WorkforceDirectoryContext,provider:UsageChannel,connection:string,q:string,page:number,includeDepartments:boolean,signal:AbortSignal,parentID?:string):Promise<ConnectionDirectoryQueryResult> {
      const key=JSON.stringify([context.workspace,context.employeeId,context.purpose||'usage',provider,connection,q,includeDepartments,parentID]);
      if(page===1)pages.set(key,{people:[''],departments:['']});
      const state=pages.get(key);if(!state)throw new Error('请从第一页重新查询通讯录');
      const peopleCursor=state.people[page-1], departmentCursor=state.departments[page-1];
      if(peopleCursor===undefined||departmentCursor===undefined)throw new Error('通讯录分页失效，请重新搜索');
      const empty={items:[] as SelectionCandidate[],next_cursor:null};
      const [users,depts]=await Promise.all([
        peopleCursor===null||(provider==='wecom'&&!q.trim())?Promise.resolve(empty):api.search(context.workspace,context.employeeId,'people',provider,q,peopleCursor,context.purpose||'usage',signal,state.snapshot,undefined,parentID),
        !includeDepartments||!['feishu','dingtalk'].includes(provider)||departmentCursor===null?Promise.resolve(empty):api.search(context.workspace,context.employeeId,'departments',provider,q,departmentCursor,context.purpose||'usage',signal,state.snapshot,parentID)
      ]);
      if(signal.aborted)throw new DOMException('读取已取消','AbortError');
      for(const [result,cursors,current] of [[users,state.people,peopleCursor],[depts,state.departments,departmentCursor]] as const){
        if(result.next_cursor&&(result.next_cursor===current||cursors.slice(0,page).includes(result.next_cursor)))throw new Error('通讯录分页游标重复，请重新搜索');
        const snapshot='snapshot_version' in result?result.snapshot_version:undefined;
        if(snapshot!==undefined){if(state.snapshot!==undefined&&snapshot!==state.snapshot)throw new Error('通讯录快照已变化，请重新搜索');state.snapshot=snapshot;}
        cursors[page]=result.next_cursor;
      }
      const userRows=users.items.filter(row=>row.ref.kind==='user'),departmentRows=depts.items.filter(row=>row.ref.kind==='department');
      // total* describe this page only; the real-context template never presents them as global totals.
      return {sourceType:provider,connectionId:connection,channelName:provider,queryMode:provider==='web'?'workspace_directory':provider==='wecom'?'live_search':'snapshot',page,pageSize:20,totalUsers:userRows.length,totalDepartments:departmentRows.length,users:userRows.map(directoryUser),departments:departmentRows.map(directoryDepartment),...(state.snapshot!==undefined?{snapshotVersion:String(state.snapshot)}:{}),...(users.next_cursor||depts.next_cursor?{nextCursor:String(page+1)}:{})};
    },
    async verify(context:WorkforceDirectoryContext,principal:DirectoryPrincipal,signal:AbortSignal):Promise<DirectoryPrincipal> {
      const original=principal.selectionCandidate;
      const result=await api.resolve(context.workspace,context.employeeId,'people',[original.ref],context.purpose||'usage',signal);
      const current=result.items.find(row=>selectorKey(row.ref)===selectorKey(original.ref));
      if(!current?.selectable)throw new Error(current?.disabled_reason||'候选身份不可用，请重新搜索');
      const ref=directoryPrincipal(current);
      if(principal.usageSelector.include_descendants!==undefined)ref.usageSelector.include_descendants=principal.usageSelector.include_descendants;
      return ref;
    }
  };
}
