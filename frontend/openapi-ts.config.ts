import { defineConfig } from '@hey-api/openapi-ts'

// The API client is generated from the OpenAPI document that the Go server
// writes with `go generate ./...`.
export default defineConfig({
  input: '../openapi/openapi.yaml',
  output: { path: 'app/api' },
  plugins: [
    { name: '@hey-api/client-fetch', throwOnError: true },
    '@hey-api/typescript',
    '@hey-api/sdk',
  ],
})
