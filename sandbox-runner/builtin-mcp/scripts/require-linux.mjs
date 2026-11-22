if (process.platform !== 'linux') {
  throw new Error(
    'Full process acceptance requires Linux; no root or UID/GID switching is needed.',
  );
}
