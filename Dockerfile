# Путь модуля для назначения версии при сборке.
ARG MODULE=github.com/ruwcs/wcs-guide
# Версия сборки; без релизного значения используется dev.
ARG VERSION=dev

# Сборка статического приложения без CGO.
FROM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG MODULE
ARG VERSION
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath \
    -ldflags "-s -w -X ${MODULE}/internal/config.version=${VERSION}" \
    -o /build/wcs-guide ./cmd/wcs-guide

# Минимальное окружение с непривилегированным пользователем.
FROM gcr.io/distroless/static:nonroot
WORKDIR /
# Адрес исходного кода приложения.
LABEL org.opencontainers.image.source="https://github.com/ruwcs/wcs-guide"
# Лицензия кода приложения.
LABEL org.opencontainers.image.licenses="AGPL-3.0-only"
COPY --from=builder /build/wcs-guide /wcs-guide
COPY LICENSE /licenses/LICENSE
# Постоянный каталог данных приложения.
VOLUME ["/data"]
# Путь к файлу базы данных.
ENV DB_PATH=/data/wcs-guide.db
# HTTP-порт приложения.
EXPOSE 8080
ENTRYPOINT ["/wcs-guide"]

# Путь к файлу сессии Telegram.
ENV TELEGRAM_SESSION_PATH=/data/telegram-bot.session
