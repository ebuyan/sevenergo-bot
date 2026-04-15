# sevenergo-bot

HTTP-сервис для автоматизации работы с личным кабинетом [sevenergosbyt.ru](https://sevenergosbyt.ru/flk/).

Позволяет получать баланс, передавать показания счётчика и инициировать оплату через ВТБ.

## Переменные окружения

| Переменная             | Обязательная | Описание                             |
|------------------------|:---:|--------------------------------------|
| `ENERGO_USERNAME`      | ✓   | Имя пользователя (ФИО)               |
| `ENERGO_LICEVOY_SCHET` | ✓   | Лицевой счёт (например, `404-000`)   |
| `ENERGO_NOMER_SCHET`   | ✓   | Номер счёта                          |
| `ENERGO_EMAIL`         | ✓   | Email для квитанций об оплате        |
| `LISTEN_ADDR`          |     | Адрес сервера (по умолчанию `:8080`) |

## API

Все ответы в формате `application/json`.

---

### `GET /status`

Возвращает текущий баланс и показания счётчика.

**Пример ответа:**
```json
{
  "dept": -245.34,
  "date": "14.04.2026",
  "last_reading": 88000,
  "last_reading_date": "01.04.2026",
  "pending_reading": 89000,
  "pending_date": "14.04.2026",
  "pending_status": "показания обрабатываются",
  "diff_kwh": 1000
}
```

- `dept < 0` — переплата, платить не нужно
- `dept > 0` — задолженность
- `pending_*` и `diff_kwh` присутствуют только если есть показания на обработке

---

### `GET /submit?value=89100`

Передаёт показания счётчика. Возвращает обновлённый статус (как `/status`).

| Параметр | Тип    | Описание                   |
|----------|--------|----------------------------|
| `value`  | number | Текущие показания счётчика |

**Валидации:**
- `value` должно быть больше последних зафиксированных (или показаний на обработке)
- Разница с зафиксированными не должна превышать 4000 кВт·ч
- После отправки проверяется, что показания появились со статусом `обрабатываются`

---

### `GET /pay?amount=100`

Инициирует оплату через ВТБ. Возвращает ссылку на страницу оплаты.

| Параметр | Тип    | Описание      |
|----------|--------|---------------|
| `amount` | number | Сумма платежа |

**Пример ответа:**
```json
{
  "redirect_url": "https://pay.vtb.ru/..."
}
```

---

### Ошибки

```json
{
  "error": "описание ошибки"
}
```

## Запуск

### Локально

```bash
export ENERGO_USERNAME="Иванов"
export ENERGO_LICEVOY_SCHET="404-000"
export ENERGO_NOMER_SCHET="12000000"
export ENERGO_EMAIL="user@mail.ru"

go run .
```

### Docker

```bash
docker build -t energo .

docker run -p 8080:8080 \
  -e ENERGO_USERNAME="Иванов" \
  -e ENERGO_LICEVOY_SCHET="404-000" \
  -e ENERGO_NOMER_SCHET="12000000" \
  -e ENERGO_EMAIL="user@mail.ru" \
  energo
```

### docker-compose

```yaml
services:
  energo:
    build: https://github.com/ebuyan/sevenergo-bot.git
    ports:
      - "8321:8080"
    environment:
      ENERGO_USERNAME: "Иванов"
      ENERGO_LICEVOY_SCHET: "404-000"
      ENERGO_NOMER_SCHET: "12000000"
      ENERGO_EMAIL: "user@mail.ru"
    restart: unless-stopped
```
