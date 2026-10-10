# .github/workflows/

CI/CD workflows.

| File | What |
| ---- | ---- |
| `docker-publish.yaml` | Docker build + test + publish on version tags |
| `security-check.yaml` | Security scan + Docker build/test on main |
| `codeql.yml` | CodeQL code scanning |
| `run-security-image-tests.yaml` | Security image tests |

Parent `.github/` also holds `dependabot.yaml` (dependency updates).
