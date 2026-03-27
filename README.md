# gitlabcloner

> ⚠️ **This utility was written using vibe coding — AI-assisted development without deep manual code review. Use at your own risk.**
>
> ⚠️ **Эта утилита написана с помощью вайбкодинга — разработка с помощью ИИ без глубокого ручного ревью кода. Используйте на свой страх и риск.**

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
Token: 
Group ID: 123
SSL verify (true/false) [true]: 
Clone dir [.]: /path/to/dir
Origin protocol (ssh/https) [ssh]: 
```

### Через переменные окружения

```bash
export GITLAB_CLONER_URL=https://gitlab.example.com
export GITLAB_CLONER_API_PATH=/api/v4
export GITLAB_CLONER_TOKEN=glpat-xxxxxxxxxxxx
export GITLAB_CLONER_GROUP_ID=123
export GITLAB_CLONER_DIR=/path/to/dir
export GITLAB_CLONER_SSL_VERIFY=true
export GITLAB_CLONER_ORIGIN_PROTO=ssh

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
| `GITLAB_CLONER_ORIGIN_PROTO` | Протокол origin: `ssh` или `https` | `ssh` |

## Поведение

- Рекурсивно обходит группу и все подгруппы.
- Сохраняет структуру директорий по `path_with_namespace`.
- Уже склонированные репозитории обновляются через `git pull --ff-only`.
- Клонирование выполняется через HTTPS с токеном; после клона origin заменяется на SSH (или чистый HTTPS без токена) — токен не сохраняется в `.git/config`.
