<script setup lang="ts">
import Direction2App from './direction2/Direction2App.vue';
import PublicTaskDetailView from './direction2/views/PublicTaskDetailView.vue';
import { publicTaskToken } from './direction2/api/public-task';
import './direction2/styles/design-tokens.css';

const publicRoute = /^\/tasks(?:\/|$)/.test(location.pathname);
const token = publicRoute ? publicTaskToken(location.pathname) || '' : '';
</script>

<template>
  <PublicTaskDetailView v-if="publicRoute" :token="token" />
  <Direction2App v-else />
</template>
