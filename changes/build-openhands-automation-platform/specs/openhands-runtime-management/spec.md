## ADDED Requirements

### Requirement: Runtime SHALL размещать внутренние компоненты платформы в Kubernetes
Runtime SHALL размещать OpenHands UI, OpenHands агентные компоненты, task observer и control plane в Kubernetes как основные runtime-компоненты платформы первой версии.

#### Scenario: Запуск внутренних компонентов платформы в Kubernetes
- **WHEN** платформа разворачивается в целевой среде
- **THEN** OpenHands UI, агентные компоненты, task observer и control plane запускаются внутри Kubernetes как часть runtime-контура

### Requirement: Runtime SHALL использовать Kubernetes Secrets для секретов первой версии
Runtime SHALL использовать Kubernetes Secrets как базовый механизм хранения и доставки секретов для OpenHands UI, агентов и связанных внутренних сервисов.

#### Scenario: Использование Kubernetes Secrets
- **WHEN** компоненту OpenHands или внутреннему сервису платформы нужен секрет для работы
- **THEN** секрет берется из Kubernetes Secrets и предоставляется компоненту через стандартные механизмы Kubernetes
