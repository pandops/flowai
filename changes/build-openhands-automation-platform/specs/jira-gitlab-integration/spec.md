## ADDED Requirements

### Requirement: Интеграции SHALL поддерживать intake задач из Jira
Платформа SHALL поддерживать intake задач из Jira как единственного источника задач первой версии через task observer.

#### Scenario: Intake задачи из Jira
- **WHEN** task observer получает задачу из Jira
- **THEN** платформа извлекает необходимые поля задачи, нормализует их и передает в control plane для реализации в OpenHands

### Requirement: Task observer SHALL оставаться расширяемым для будущих источников задач
Платформа SHALL закладывать в task observer совместимый adapter contract для будущих источников задач, не требуя их реализации в первой версии.

#### Scenario: Добавление нового источника в будущем
- **WHEN** команда решает подключить новый источник задач после MVP
- **THEN** новый источник может быть интегрирован через adapter contract task observer без изменения GitLab workflow и без изменения контракта control plane

### Requirement: OpenHands executor SHALL взаимодействовать с GitLab напрямую
Платформа SHALL позволять OpenHands execution напрямую взаимодействовать с GitLab для branch workflow и merge request в рамках поддерживаемого сценария.

### Requirement: Интеграции SHALL поддерживать MR-ориентированный GitLab workflow
Платформа SHALL поддерживать как минимум чтение состояния репозитория, подготовку change set в разрешенной рабочей ветке, публикацию изменений в GitLab и создание merge request как основной write-path для изменений.

#### Scenario: Публикация изменений в разрешенную ветку
- **WHEN** implementation stage подготовила change set для разрешенного GitLab-репозитория
- **THEN** OpenHands execution публикует изменения только в согласованную рабочую ветку напрямую в GitLab

#### Scenario: Создание merge request по результату автоматизации
- **WHEN** review stage успешно завершилась для изменений в разрешенном GitLab-репозитории
- **THEN** OpenHands execution открывает merge request в GitLab для дальнейшей human review
