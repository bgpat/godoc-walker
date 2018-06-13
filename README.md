# godoc-walker
Refresh godoc in all of owned repositories

## Usage

```bash
# job mode (require Redis)
godoc-walker
```

or

```bash
# manual mode
godoc-walker [repo url ...]
```

## Envs

var | default | description | example
-- | -- | -- | --
GITHUB_ACCESSS_TOKEN | (required) | accesss token for GitHub |
GITHUB_TOKEN | | access token for GitHub (when `$GITHUB_ACCESS_TOKEN` is not set) |
GITHUB_ORGANIZATION | `""` | GitHub organization to crawl repositories | wantedly
GODOC_URL | `"https://godoc.org"` | URL of godoc | https://godoc.401.jp
GODOC_REQUEST_TIMEOUT | `""` | request timeout to access to godoc | 5m
REDIS_URL | `"redis://localhost:6379"` | URL for Redis | redis://redis:6379/1
