<script setup lang="ts">
import type { AssistantCard } from '../api/assistant';

defineProps<{ cards: AssistantCard[]; disabled?: boolean; hideNote?: boolean }>();
defineEmits<{ choose: [card: AssistantCard] }>();

interface CardMeta {
  tag: string;
  badge: string;
  badgeType: 'recommend' | 'drill' | '';
  cta: string;
  tone: 'blue' | 'emerald' | 'amber';
}

const CARD_META_MAP: Record<string, CardMeta> = {
  complaints: {
    tag: '并行推进两单',
    badge: '推荐',
    badgeType: 'recommend',
    cta: '同时跟进两单',
    tone: 'blue',
  },
  meeting: {
    tag: '人工确认续跑',
    badge: '',
    badgeType: '',
    cta: '开始跟进',
    tone: 'emerald',
  },
  incident: {
    tag: '自主排查恢复',
    badge: '演练',
    badgeType: 'drill',
    cta: '开始恢复',
    tone: 'amber',
  },
};

const DEFAULT_META: CardMeta = {
  tag: '真实任务流程',
  badge: '',
  badgeType: '',
  cta: '立即体验',
  tone: 'blue',
};

function meta(key: string): CardMeta {
  return CARD_META_MAP[key] ?? DEFAULT_META;
}
</script>

<template>
  <div class="assistant-demo-cards" aria-label="体验场景">
    <p v-if="!hideNote" class="assistant-demo-note">使用内置样例数据，体验真实任务拆分、人工确认与主动回告。</p>
    <div class="demo-cards-grid">
      <button
        v-for="card in cards"
        :key="card.key"
        type="button"
        class="demo-card"
        :data-tone="meta(card.key).tone"
        :disabled="disabled"
        @click="$emit('choose', card)"
      >
        <div class="demo-card-top">
          <span class="demo-card-tag-group">
            <span class="demo-card-icon" aria-hidden="true">
              <svg v-if="card.key === 'complaints'" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M16 3h5v5" />
                <path d="M8 3H3v5" />
                <path d="M12 22v-8.3a4 4 0 0 0-1.172-2.872L3 3" />
                <path d="m15 9 6-6" />
              </svg>
              <svg v-else-if="card.key === 'meeting'" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M9 11l3 3L22 4" />
                <path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11" />
              </svg>
              <svg v-else-if="card.key === 'incident'" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z" />
              </svg>
              <svg v-else width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <polygon points="6 3 20 12 6 21 6 3" />
              </svg>
            </span>
            <span class="demo-card-tag">{{ meta(card.key).tag }}</span>
          </span>
          <span
            v-if="meta(card.key).badge"
            class="demo-card-badge"
            :class="meta(card.key).badgeType"
          >
            {{ meta(card.key).badge }}
          </span>
        </div>

        <div class="demo-card-body">
          <strong class="demo-card-title">{{ card.title }}</strong>
          <small class="demo-card-desc">{{ card.description }}</small>
        </div>

        <div class="demo-card-footer">
          <span class="demo-card-cta">
            {{ meta(card.key).cta }}
            <span class="demo-card-arrow" aria-hidden="true">→</span>
          </span>
        </div>
      </button>
    </div>
  </div>
</template>

<style scoped>
.assistant-demo-cards {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.assistant-demo-note {
  margin: 0;
  color: var(--id-text-muted, #64748b);
  font-size: 12px;
  line-height: 1.5;
  text-align: left;
}

.demo-cards-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
  gap: 14px;
  width: 100%;
}

.demo-card {
  --card-accent: var(--id-accent-primary, #2563eb);
  --card-soft: rgba(37, 99, 235, 0.09);
  --card-ink: #1d4ed8;
  appearance: none;
  font-family: inherit;
  width: 100%;
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  gap: 12px;
  padding: 16px;
  border-radius: 12px;
  border: 1px solid var(--id-border-subtle, #e2e8f0);
  background: var(--id-bg-surface, #ffffff);
  color: var(--id-text-primary, #0f172a);
  text-align: left;
  cursor: pointer;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  transition:
    border-color 0.16s ease,
    box-shadow 0.16s ease,
    transform 0.16s ease,
    background-color 0.16s ease;
}

.demo-card[data-tone='emerald'] {
  --card-accent: #059669;
  --card-soft: rgba(16, 185, 129, 0.10);
  --card-ink: #047857;
}

.demo-card[data-tone='amber'] {
  --card-accent: #d97706;
  --card-soft: rgba(245, 158, 11, 0.11);
  --card-ink: #b45309;
}

.demo-card:hover:not(:disabled) {
  border-color: var(--id-accent-primary, #2563eb);
  box-shadow: 0 6px 18px rgba(37, 99, 235, 0.09);
  transform: translateY(-1px);
}

.demo-card:active:not(:disabled) {
  transform: translateY(0);
}

.demo-card:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring, 0 0 0 2px rgba(37, 99, 235, 0.25));
}

.demo-card:disabled {
  opacity: 0.56;
  cursor: not-allowed;
}

.demo-card-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.demo-card-tag-group {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}

.demo-card-icon {
  width: 26px;
  height: 26px;
  border-radius: 7px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: var(--card-soft);
  color: var(--card-ink);
  flex-shrink: 0;
}

.demo-card-tag {
  font-size: 12px;
  font-weight: 500;
  color: var(--id-text-secondary, #52525b);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.demo-card-badge {
  flex-shrink: 0;
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
  padding: 4px 8px;
  border-radius: 999px;
}

.demo-card-badge.recommend {
  background: rgba(37, 99, 235, 0.10);
  color: #1d4ed8;
  border: 1px solid rgba(37, 99, 235, 0.20);
}

.demo-card-badge.drill {
  background: rgba(245, 158, 11, 0.11);
  color: #b45309;
  border: 1px solid rgba(245, 158, 11, 0.24);
}

.demo-card-body {
  display: flex;
  flex-direction: column;
  gap: 6px;
  flex: 1;
}

.demo-card-title {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--id-text-primary, #09090b);
}

.demo-card-desc {
  font-size: 13px;
  line-height: 1.55;
  color: var(--id-text-secondary, #526071);
}

.demo-card-footer {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  padding-top: 10px;
  border-top: 1px solid var(--id-border-hairline, #f1f5f9);
}

.demo-card-cta {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 13px;
  font-weight: 600;
  color: var(--id-accent-primary, #2563eb);
}

.demo-card-arrow {
  transition: transform 0.16s ease;
}

.demo-card:hover:not(:disabled) .demo-card-arrow {
  transform: translateX(3px);
}
</style>
