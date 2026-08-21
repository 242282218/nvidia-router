import eslint from '@eslint/js'
import vue from 'eslint-plugin-vue'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  ...vue.configs['flat/recommended'],
  {
    files: ['**/*.vue'],
    languageOptions: {
      globals: {
        AbortController: 'readonly',
        clearInterval: 'readonly',
        clearTimeout: 'readonly',
        setInterval: 'readonly',
        setTimeout: 'readonly',
      },
      parserOptions: {
        parser: tseslint.parser,
      },
    },
  },
  {
    // The shared StatePanel/LoadingSpinner props are deliberately passed in
    // camelCase: the kebab-case form (`error-testid`) failed to resolve onto
    // the declared prop in the test runtime (reproduced in StatePanel.spec),
    // while camelCase works in both vitest and the browser build. Hyphenating
    // these would silently drop the data-testid wiring the e2e suite relies on.
    files: ['**/*.vue'],
    rules: {
      'vue/attribute-hyphenation': ['error', 'always', {
        ignore: ['loadingLabel', 'emptyLabel', 'emptyHint', 'retryLabel', 'errorTestId', 'retryTestId', 'showLabel', 'ariaLabel'],
      }],
    },
  },
  {
    ignores: ['dist/**'],
  },
)
