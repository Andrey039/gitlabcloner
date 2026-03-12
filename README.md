# gitlabcloner

Go-утилита для рекурсивного клонирования всех репозиториев GitLab-группы (включая подгруппы).

## Требования

- Go 1.21+
- `git` в `PATH`
- Приватный токен GitLab с правами `read_api` и `read_repository`

## Сборка

```bash
make
```

или вручную:

```bash
go build -o gitlabcloner .
```

## Использование

### Интерактивный режим

```bash
./gitlabcloner
```

Утилита запросит параметры интерактивно:

```
GitLab URL [https://gitlab.com]: 
GitLab API path [/api/v4]: 
GitLab private token: 
GitLab group ID: 123
Clone directory [.]: /path/to/dir
Verify SSL [true]: 
```

### Через переменные окружения

```bash
export GITLAB_CLONER_URL=https://gitlab.example.com
export GITLAB_CLONER_API_PATH=/api/v4
export GITLAB_CLONER_TOKEN=glpat-xxxxxxxxxxxx
export GITLAB_CLONER_GROUP_ID=123
export GITLAB_CLONER_DIR=/path/to/dir
export GITLAB_CLONER_SSL_VERIFY=true

./gitlabcloner
```

Если переменная окружения задана — утилита не запрашивает значение интерактивно.

## Переменные окружения

| Переменная | Описание | По умолчанию |
|---|---|---|
| `GITLAB_CLONER_URL` | URL GitLab-инстанса | `https://gitlab.com` |
| `GITLAB_CLONER_API_PATH` | Путь до API | `/api/v4` |
| `GITLAB_CLONER_TOKEN` | Приватный токен | — |
| `GITLAB_CLONER_GROUP_ID` | ID группы | — |
| `GITLAB_CLONER_DIR` | Директория для клонирования | `.` (текущая) |
| `GITLAB_CLONER_SSL_VERIFY` | Проверка SSL-сертификата | `true` |

## Поведение

- Рекурсивно обходит группу и все подгруппы.
- Сохраняет структуру директорий по `path_with_namespace`.
- Уже склонированные репозитории (наличие `.git`) пропускаются.
- Для клонирования использует `https://oauth2:<token>@<repo_url>`.
