import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import globals from 'globals';

// The component is invoked by the repository's unified lint entry. Keeping
// its parser project here also allows an isolated artifact to be checked.
export default tseslint.config(
  {
    ignores: ['node_modules/**', 'dist/**', 'vendor/**'],
    linterOptions: { reportUnusedDisableDirectives: 'error' },
  },
  {
    files: ['**/*.ts'],
    extends: [js.configs.recommended, ...tseslint.configs.recommendedTypeChecked],
    languageOptions: {
      globals: globals.node,
      parserOptions: {
        project: './tsconfig.json',
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      complexity: ['error', { max: 35, variant: 'classic' }],
      'max-depth': ['error', 6],
      '@typescript-eslint/no-floating-promises': ['error', { ignoreVoid: false }],
      '@typescript-eslint/no-misused-promises': 'error',
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/ban-ts-comment': [
        'error',
        {
          'ts-ignore': true,
          'ts-nocheck': true,
          'ts-check': false,
          'ts-expect-error': 'allow-with-description',
          minimumDescriptionLength: 10,
        },
      ],
      '@typescript-eslint/switch-exhaustiveness-check': 'error',
    },
  },
  {
    files: ['**/*.mjs'],
    extends: [js.configs.recommended],
    languageOptions: { globals: globals.node },
    rules: {
      complexity: ['error', { max: 20, variant: 'classic' }],
      'max-depth': ['error', 4],
    },
  },
  {
    files: ['tests/**/*.mjs'],
    rules: { complexity: 'off', 'max-depth': 'off' },
  },
);
