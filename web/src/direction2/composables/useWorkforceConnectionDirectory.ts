import type { Ref } from 'vue';
import type { WorkforcePorts } from '../api/workforce-ports';
import type {
  ChannelDirectoryDepartment, ChannelDirectoryUser, ChannelSyncSourceDetail,
  ConnectionDirectoryQueryResult, DigitalEmployee, PlatformType
} from '../types';
import { mergeConnectionDirectoryData } from '../utils/workforce';

export function useWorkforceConnectionDirectory({
  resourcesApi, employees, directoryUsers, directoryDepartments
}: {
  resourcesApi: Pick<WorkforcePorts['resourcesApi'],
    'queryWebDirectory' | 'queryChannelSnapshotDirectory' | 'searchWecomConnectionDirectory'>;
  employees: Ref<DigitalEmployee[]>;
  directoryUsers: Ref<ChannelDirectoryUser[]>;
  directoryDepartments: Ref<ChannelDirectoryDepartment[]>;
}) {
  function mergeConnectionDirectoryResult(res: ConnectionDirectoryQueryResult) {
    const merged = mergeConnectionDirectoryData(directoryUsers.value, directoryDepartments.value, res);
    directoryUsers.value = merged.users;
    directoryDepartments.value = merged.departments;
  }

  async function preloadConnectionScopedDirectory(sources: ChannelSyncSourceDetail[]) {
    directoryUsers.value = [];
    const connTasks: Promise<ConnectionDirectoryQueryResult>[] = [
      resourcesApi.queryWebDirectory({ page: 1, pageSize: 20 })
    ];
    const seenConns = new Set<string>();
    const enqueueConn = (channel: PlatformType, connectionId?: string | undefined) => {
      const cleanConn = connectionId?.trim();
      if (!cleanConn) return;
      const key = `${channel}::${cleanConn}`;
      if (seenConns.has(key)) return;
      seenConns.add(key);
      if (channel === 'feishu' || channel === 'dingtalk') {
        connTasks.push(resourcesApi.queryChannelSnapshotDirectory(channel, cleanConn, { page: 1, pageSize: 20 }));
      } else if (channel === 'wecom') {
        connTasks.push(resourcesApi.searchWecomConnectionDirectory(cleanConn, { page: 1, pageSize: 20 }));
      }
    };

    for (const src of sources) {
      enqueueConn(src.channel, src.connectionId);
    }
    for (const emp of employees.value) {
      for (const ch of emp.channels) {
        enqueueConn(ch.platform, ch.connectionId);
      }
    }

    const results = await Promise.allSettled(connTasks);
    for (const r of results) {
      if (r.status === 'fulfilled') {
        mergeConnectionDirectoryResult(r.value);
      }
    }
  }

  return { mergeConnectionDirectoryResult, preloadConnectionScopedDirectory };
}
