import { computed, h, inject, onUnmounted, ref, watch, type Ref } from 'vue';
import { NButton, useDialog, type DialogReactive } from 'naive-ui';
import { activeWorkspaceId } from '../api/client';
import { platformRequest } from '../api/platform';
import type { Page } from '../api/capabilities';
export function useResourcePanel<T>(label:string,list:(w:string,p:number,s:AbortSignal)=>Promise<Page<T>>) {
 const items=ref<T[]>([]) as Ref<T[]>; const role=ref(''); const loading=ref(false); const error=ref(''); const page=ref(1); const total=ref(0);
 let epoch=0; let controller:AbortController|undefined;
 const canManage=computed(()=>['owner','admin'].includes(role.value));
 async function load(){const current=++epoch;controller?.abort();controller=new AbortController(); const w=activeWorkspaceId.value; items.value=[];role.value='';error.value='';loading.value=true;if(!w){loading.value=false;return;}
 try{const [result,info]=await Promise.all([list(w,page.value,controller.signal),platformRequest<{role:string}>(`/workspaces/${encodeURIComponent(w)}`,{signal:controller.signal})]);if(current!==epoch)return;items.value=result.items;total.value=result.total;role.value=info.role;}catch(e){if(current===epoch)error.value=e instanceof Error?e.message:`${label}加载失败`;}finally{if(current===epoch)loading.value=false;}}
 watch(activeWorkspaceId,()=>{page.value=1;void load();},{immediate:true});watch(page,()=>void load());onUnmounted(()=>{++epoch;controller?.abort();});
 return {items,role,loading,error,page,total,canManage,load};
}
export function useDraftClose(
 label: string, show: Ref<boolean>, dirty: Ref<boolean>, busy: Ref<boolean>,
 discard: () => void, save: () => Promise<boolean>, canSave: () => boolean = () => true
) {
 const dialog = useDialog();
 let confirmation: DialogReactive | undefined;
 function dismiss() { confirmation?.destroy(); confirmation = undefined; }
 const close = () => {
  if (!show.value || busy.value || confirmation) return;
  if (!dirty.value) { discard(); return; }
  const opened = dialog.warning({
   title: '有未保存的修改',
   content: '可以保存后关闭，或放弃本次修改。',
   onAfterLeave: () => { if (confirmation === opened) confirmation = undefined; },
   action: () => h('div', { style: { display: 'flex', flexWrap: 'wrap', gap: '8px' } }, [
    h(NButton, { size: 'small', disabled: busy.value, onClick: dismiss }, () => '继续编辑'),
    h(NButton, { size: 'small', disabled: busy.value, onClick: () => { dismiss(); discard(); } }, () => '放弃修改'),
    h(NButton, {
     size: 'small', type: 'primary', loading: busy.value, disabled: busy.value || !canSave(),
     onClick: async () => {
      if (busy.value || !canSave() || !show.value) return;
      const saved = await save();
      dismiss();
      if (saved && show.value) discard();
     }
    }, () => '保存并关闭'),
   ])
  });
  confirmation = opened;
 };
 const draft = { hasUnsaved: () => show.value && (dirty.value || busy.value), getLabel: () => label, save, discard };
 const guard = inject<{register: (d: typeof draft) => void; unregister: (d: typeof draft) => void }>('d2DraftGuard');
 guard?.register(draft);
 watch(show, visible => { if (!visible) dismiss(); });
 onUnmounted(() => { dismiss(); guard?.unregister(draft); });
 return close;
}
export function base64Bytes(value:string):Uint8Array {return Uint8Array.from(atob(value),c=>c.charCodeAt(0));}
export function bytesBase64(bytes:Uint8Array):string {let out='';for(let i=0;i<bytes.length;i+=8192)out+=String.fromCharCode(...bytes.subarray(i,i+8192));return btoa(out);}
