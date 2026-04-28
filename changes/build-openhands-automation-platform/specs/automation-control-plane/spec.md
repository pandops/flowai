## ADDED Requirements

### Requirement: Платформа SHALL управлять выполнением задачи через единый control plane
Платформа SHALL принимать нормализованную задачу через единый control plane, выбирать поддерживаемый сценарий и запускать единый execution в OpenHands.

#### Scenario: Успешный запуск поддерживаемого сценария
- **WHEN** task observer передает в control plane нормализованную задачу для поддерживаемого сценария автоматизации
- **THEN** control plane создает execution context и запускает implementation stage

#### Scenario: Отклонение неподдерживаемого сценария
- **WHEN** task observer передает задачу, для которой не существует поддерживаемого сценария автоматизации
- **THEN** control plane отклоняет задачу без запуска execution

### Requirement: Платформа SHALL получать задачи через task observer из Jira
Платформа SHALL предоставлять task observer, который получает задачи из Jira, нормализует их в единый внутренний формат и передает их в control plane для дальнейшего выполнения. Внутренняя архитектура task observer SHALL допускать подключение дополнительных источников задач без изменения базового execution flow.

#### Scenario: Прием задачи из Jira
- **WHEN** в Jira появляется или обновляется задача, удовлетворяющая правилам наблюдения
- **THEN** task observer получает задачу, нормализует ее в единый внутренний формат и передает ее в control plane

#### Scenario: Подготовка к будущему источнику задач
- **WHEN** в платформу позже добавляется новый источник задач
- **THEN** task observer может подключить его через совместимый adapter contract без изменения контракта между task observer и control plane


### Requirement: Платформа SHALL выполнять implementation и review как две стадии одного execution
Платформа SHALL запускать реализацию изменений как `stage 1: implement`, после чего SHALL запускать `stage 2: review` в рамках того же execution context до открытия merge request.

#### Scenario: Переход от implementation к review
- **WHEN** implementation stage завершила подготовку изменений в рабочей ветке
- **THEN** control plane запускает review stage в рамках того же execution

#### Scenario: Review stage проходит успешно
- **WHEN** review stage не выявляет блокирующих проблем
- **THEN** control plane разрешает открытие merge request

#### Scenario: Review stage выявляет проблемы
- **WHEN** review stage выявляет проблемы в изменениях
- **THEN** control plane не открывает merge request и повторно направляет задачу в implementation stage
