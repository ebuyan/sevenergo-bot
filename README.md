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
    build: https://github.com/ebuyan/sevenergo-bot.git#main
    ports:
      - "8321:8080"
    environment:
      ENERGO_USERNAME: "Иванов"
      ENERGO_LICEVOY_SCHET: "404-000"
      ENERGO_NOMER_SCHET: "12000000"
      ENERGO_EMAIL: "user@mail.ru"
    restart: unless-stopped
```

---

## Интеграция с Home Assistant

### Требования

- Home Assistant 2023.4+
- Сервис доступен из HA по сети (например, в одном docker-compose)
- Настроена интеграция уведомлений (например, `notify.mobile_app_<device>`)

---

### 1. REST-сенсор (`configuration.yaml`)

Опрашивает `/status` раз в сутки и хранит данные как атрибуты сенсора.

```yaml
sensor:
  - platform: rest
    name: "energo"
    resource: "http://energo:8080/status"
    scan_interval: 86400
    value_template: "{{ value_json.dept }}"
    unit_of_measurement: "руб"
    json_attributes:
      - dept
      - date
      - last_reading
      - last_reading_date
      - pending_reading
      - pending_date
      - diff_kwh
```

---

### 2. Вспомогательный helper для показаний (`configuration.yaml`)

Сюда вручную вводится текущее значение счётчика перед ежемесячной подачей.

```yaml
input_number:
  energo_meter_reading:
    name: "Показания счётчика (электро)"
    min: 0
    max: 999999
    step: 1
    unit_of_measurement: "кВт·ч"
    mode: box
```

---

### 3. REST-команды (`configuration.yaml`)

```yaml
rest_command:
  energo_pay:
    url: "http://energo:8080/pay?amount={{ amount }}"
    method: GET

  energo_submit:
    url: "http://energo:8080/submit?value={{ value }}"
    method: GET
```

---

### 4. Автоматизация ошибки сенсора (`automations.yaml`)

Если `/status` вернул ошибку или сервис недоступен, сенсор переходит в состояние `unavailable`. Уведомляем об этом.

```yaml
- alias: "Энергосбыт: ошибка получения статуса"
  trigger:
    - platform: state
      entity_id: sensor.energo
      to: "unavailable"
    - platform: state
      entity_id: sensor.energo
      to: "unknown"
  action:
    - action: notify.mobile_app_YOUR_DEVICE
      data:
        title: "⚡ Энергосбыт: ошибка"
        message: "Не удалось получить статус. Сервис недоступен или вернул ошибку."
```

---

### 5. Автоматизации (`automations.yaml`)

#### Ежедневная проверка долга + ссылка на оплату

Запускается в 9:00. Если `dept > 0` — получает ссылку на оплату,
сумма округляется вверх до ближайших 100 руб.

```yaml
- alias: "Энергосбыт: проверка долга"
  trigger:
    - platform: time
      at: "09:00:00"
  condition:
    - condition: template
      value_template: "{{ state_attr('sensor.energo', 'dept') | float(0) > 0 }}"
  action:
    - action: homeassistant.update_entity
      target:
        entity_id: sensor.energo
    - variables:
        debt: "{{ state_attr('sensor.energo', 'dept') | float }}"
        amount: "{{ (debt / 100) | round(0, 'ceil') | int * 100 }}"
    - action: rest_command.energo_pay
      data:
        amount: "{{ amount }}"
      response_variable: pay_response
    - action: notify.mobile_app_YOUR_DEVICE
      data:
        title: "⚡ Долг за электроэнергию"
        message: "Задолженность {{ debt }} руб. Ссылка на оплату {{ amount }} руб."
        data:
          url: "{{ pay_response['content']['redirect_url'] }}"
```

---

#### Ежедневный дифф показаний

Отправляет уведомление с разницей, если показания на обработке есть.

```yaml
- alias: "Энергосбыт: расход за период"
  trigger:
    - platform: time
      at: "09:00:00"
  condition:
    - condition: template
      value_template: "{{ state_attr('sensor.energo', 'diff_kwh') is not none }}"
  action:
    - action: notify.mobile_app_YOUR_DEVICE
      data:
        title: "⚡ Расход электроэнергии"
        message: >
          Зафиксировано: {{ state_attr('sensor.energo', 'last_reading') | int }} кВт·ч
          от {{ state_attr('sensor.energo', 'last_reading_date') }}.
          На обработке: {{ state_attr('sensor.energo', 'pending_reading') | int }} кВт·ч
          от {{ state_attr('sensor.energo', 'pending_date') }}.
          Разница: {{ state_attr('sensor.energo', 'diff_kwh') }} кВт·ч.
```

---

#### Ежемесячная подача показаний

Запускается 25-го числа каждого месяца в 10:00.
Перед этим нужно вручную обновить `input_number.energo_meter_reading` в HA.

```yaml
- alias: "Энергосбыт: подача показаний"
  trigger:
    - platform: time
      at: "10:00:00"
  condition:
    - condition: template
      value_template: "{{ now().day == 25 }}"
  action:
    - variables:
        value: "{{ states('input_number.energo_meter_reading') | int }}"
    - action: rest_command.energo_submit
      data:
        value: "{{ value }}"
      response_variable: submit_response
    - choose:
        - conditions:
            - condition: template
              value_template: "{{ submit_response['status'] == 200 }}"
          sequence:
            - action: notify.mobile_app_YOUR_DEVICE
              data:
                title: "⚡ Показания переданы"
                message: >
                  Показания {{ value }} кВт·ч успешно переданы.
                  Разница: {{ submit_response['content']['diff_kwh'] }} кВт·ч.
      default:
        - action: notify.mobile_app_YOUR_DEVICE
          data:
            title: "⚡ Ошибка подачи показаний"
            message: "{{ submit_response['content']['error'] }}"
```

---

### Примечания

- Замените `notify.mobile_app_YOUR_DEVICE` на имя вашей службы уведомлений
- `scan_interval: 86400` означает фоновый опрос раз в сутки; автоматизации с долгом и диффом дополнительно вызывают `update_entity` перед проверкой, чтобы данные были свежими
- Показания подаются вручную через карточку в HA: **Настройки → Устройства и объекты → Помощники → Показания счётчика**
