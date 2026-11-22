import { onMounted, onUnmounted, ref, type Ref } from 'vue';
import { activeWorkspaceId } from '../api/client';
import { realPlatform } from '../api/platform';
import type { WorkforcePorts } from '../api/workforce-ports';
import type { DigitalEmployee, PlatformType } from '../types';
import { appendObservedChannelBindings, patchChannelRuntimeStatusFields } from '../utils/workforce';

function getTransitionalChannels(target: DigitalEmployee): PlatformType[] {
  return target.channels
    .filter(ch =>
      !ch.configurationChanged &&
      ch.status !== 'disabled' &&
      (
        ch.status !== 'connected' ||
        ch.connectionState !== 'connected' ||
        ch.directoryStatus?.status === 'syncing'
      )
    )
    .map(ch => ch.platform);
}

function getBoundChannels(target: DigitalEmployee): PlatformType[] {
  return target.channels
    .filter(ch => !ch.configurationChanged)
    .map(ch => ch.platform);
}

export function useWorkforceChannelStatuses({
  employee, employees, snapshot, getStatuses, onSnapshotPatched
}: {
  employee: Ref<DigitalEmployee | null>;
  employees: Ref<DigitalEmployee[]>;
  snapshot: Ref<string>;
  getStatuses: WorkforcePorts['workforceApi']['getEmployeeChannelStatuses'];
  onSnapshotPatched: () => void;
}) {
  let timer: ReturnType<typeof setInterval> | undefined;
  let inFlight: AbortController | undefined;
  const retryingChannel = ref<PlatformType>();

  function stopChannelStatusPolling() {
    inFlight?.abort();
    inFlight = undefined;
    retryingChannel.value = undefined;
    clearInterval(timer);
    timer = undefined;
  }

  async function fetchStatuses(options: { provider?: PlatformType | undefined; channels?: PlatformType[] | undefined } = {}) {
    const { provider, channels } = options;
    const current = employee.value;
    if (!current || retryingChannel.value || (!provider && (inFlight || document.hidden))) return;
    if (!provider && !channels?.length) return;
    const employeeId = current.id;
    const workspaceId = activeWorkspaceId.value;
    const observedRevisions = { ...current.resourceVersions?.channels };
    inFlight?.abort();
    const request = new AbortController();
    inFlight = request;
    retryingChannel.value = provider;
    try {
      const result = await getStatuses(employeeId, workspaceId, {
        channel: provider,
        channels,
        signal: request.signal
      });
      if (request.signal.aborted || employee.value?.id !== employeeId || activeWorkspaceId.value !== workspaceId) return;

      function apply(target: DigitalEmployee) {
        if (!provider) target.status = result.status;
        if (realPlatform) {
          const errors = { ...target.channelReadErrors };
          for (const [channel, error] of Object.entries(result.channelReadErrors ?? {}) as [PlatformType, string | null][]) {
            if (target.resourceVersions?.channels[channel] !== observedRevisions[channel]) continue;
            if (error === null) delete errors[channel];
            else errors[channel] = error;
          }
          if (Object.keys(errors).length) target.channelReadErrors = errors;
          else delete target.channelReadErrors;
          appendObservedChannelBindings(target, result.addedBindings?.filter(({channel}) =>
            target.resourceVersions?.channels[channel.platform] === observedRevisions[channel.platform]));
        }
        for (const observation of result.channels) {
          const channel = target.channels.find(item => item.platform === observation.platform);
          if (!channel) continue;
          if (realPlatform) {
            const revision = target.resourceVersions?.channels?.[channel.platform];
            if (revision !== observedRevisions[channel.platform] || channel.configurationChanged) continue;
            const sameBinding = channel.connectionId === observation.connectionId;
            if (observation.bindingMissing || (observation.observedConfigRevision !== undefined
              && (!sameBinding || revision !== observation.observedConfigRevision))) {
              channel.configurationChanged = true;
              patchChannelRuntimeStatusFields(channel, {
                status: channel.status === 'disabled' ? 'disabled' : 'pending_config',
                statusText: '配置已变更',
                connectionState: 'disconnected',
                permissionReady: undefined,
                businessCapabilities: undefined,
                directoryStatus: undefined,
                identityConfirmed: undefined,
                lastVerifiedAt: ''
              });
              continue;
            }
            if (!sameBinding) continue;
          }
          patchChannelRuntimeStatusFields(channel, observation);
        }
      }

      apply(employee.value);
      const listed = employees.value.find(item => item.id === employeeId);
      if (listed) apply(listed);
      if (snapshot.value) {
        const saved = JSON.parse(snapshot.value) as DigitalEmployee;
        apply(saved);
        snapshot.value = JSON.stringify(saved);
        onSnapshotPatched();
      }
    } catch {
      // A failed background read must leave the current drafts intact.
    } finally {
      if (inFlight === request) {
        inFlight = undefined;
        retryingChannel.value = undefined;
      }
    }
  }
  const pollInterval = () => {
    const current = employee.value;
    if (!current) return;
    const channels = getTransitionalChannels(current);
    if (channels.length) void fetchStatuses({ channels });
  };

  const pollFocus = () => {
    const current = employee.value;
    if (!current) return;
    const channels = getBoundChannels(current);
    if (channels.length) void fetchStatuses({ channels });
  };

  const retryChannel = (provider?: PlatformType) =>
    fetchStatuses(provider ? { provider } : { channels: employee.value ? getBoundChannels(employee.value) : [] });

  function startChannelStatusPolling() {
    stopChannelStatusPolling();
    if (!realPlatform) pollFocus();
    timer = setInterval(pollInterval, 4000);
  }

  onMounted(() => {
    window.addEventListener('focus', pollFocus);
    document.addEventListener('visibilitychange', pollFocus);
    startChannelStatusPolling();
  });
  onUnmounted(() => {
    window.removeEventListener('focus', pollFocus);
    document.removeEventListener('visibilitychange', pollFocus);
    stopChannelStatusPolling();
  });

  return { startChannelStatusPolling, stopChannelStatusPolling, retryChannel, retryingChannel };
}
