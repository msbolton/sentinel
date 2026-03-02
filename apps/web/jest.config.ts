export default {
  displayName: 'web',
  preset: 'jest-preset-angular',
  setupFilesAfterEnv: ['<rootDir>/src/test-setup.ts'],
  testMatch: ['<rootDir>/src/**/*.spec.ts'],
  coverageDirectory: '../../coverage/apps/web',
  moduleNameMapper: {
    '^@sentinel/(.*)$': '<rootDir>/../../libs/$1/src/index.ts',
  },
};
