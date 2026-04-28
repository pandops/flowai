## Why

Организации нужен внутренний контур для автоматизации легких и средних рутинных задач разработки и сопровождения. OpenHands SDK дает основу для агентного исполнения, но сам по себе не решает вопросы размещения OpenHands UI и агентов в Kubernetes, приема задач из Jira, интеграции с GitLab и базовых ограничений на выполнение.

## What Changes

- Создать внутреннюю платформу-оркестратор на базе OpenHands SDK для запуска типовых автоматизаций.
- Ввести runtime-контур для OpenHands в Kubernetes, где OpenHands UI, агенты и вспомогательные компоненты платформы запускаются внутри кластера, а секреты хранятся в Kubernetes Secrets.
- Добавить `task observer`, который получает задачи из Jira, нормализует их и передает в control plane для запуска единого execution в OpenHands, при этом его внутренняя архитектура должна допускать подключение дополнительных источников задач в будущем.
- Зафиксировать прямое взаимодействие OpenHands executor с GitLab в рамках branch workflow и merge request.
- Определить единый контракт жизненного цикла задачи: intake из внешнего источника, нормализация, implementation stage, review stage, открытие merge request и human review.
- Зафиксировать границы первой версии: автоматизация легких и средних сценариев, прежде всего связанных с задачами Jira и изменениями в GitLab.

## Capabilities

### New Capabilities
- `automation-control-plane`: оркестрация жизненного цикла автоматизаций, intake задач через task observer и управление staged execution в OpenHands.
- `openhands-runtime-management`: управление OpenHands runtime в Kubernetes, включая размещение UI и агентов, sandbox-изоляцию и хранение секретов в Kubernetes Secrets.
- `jira-gitlab-integration`: прием задач из Jira через task observer с возможностью расширения на другие источники в будущем, а также операции OpenHands executor с GitLab для branch workflow и merge request.

### Modified Capabilities

_Нет._

## Impact

- OpenHands SDK и remote agent server/runtime model.
- Kubernetes-инфраструктура для исполнения runtime и сопутствующих сервисов.
- Интеграции с Jira API и GitLab API.
- Kubernetes Secrets, RBAC и runtime-компоненты OpenHands.
- Будущие сервисы task observer и control plane, включая расширение task observer новыми source adapters.
