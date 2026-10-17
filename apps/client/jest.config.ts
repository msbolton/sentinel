export default {
  displayName: 'client',
  preset: 'jest-preset-angular',
  setupFilesAfterEnv: ['<rootDir>/src/test-setup.ts'],
  testMatch: ['<rootDir>/src/**/*.spec.ts'],
  coverageDirectory: '../../coverage/apps/client',
  moduleNameMapper: {
    '^@sentinel/(.*)$': '<rootDir>/../../libs/$1/src/index.ts',
    // satellite.js exports only an "import" entry; point Jest at the file.
    '^satellite\\.js$': '<rootDir>/../../node_modules/satellite.js/dist/index.js',
  },
  // satellite.js ships ESM only; let the Angular transformer compile it.
  transformIgnorePatterns: ['node_modules/(?!(.*\\.mjs$|satellite\\.js/|@angular/common/locales/.*\\.js$))'],
};
