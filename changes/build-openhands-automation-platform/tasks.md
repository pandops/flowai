## 1. Product and Architecture Foundation

- [ ] 1.1 Уточнить и зафиксировать MVP-каталог автоматизаций, роли пользователей и границы первой версии платформы.
- [ ] 1.2 Спроектировать доменную модель task observer, control plane и каталога сценариев автоматизации.
- [ ] 1.3 Выбрать контракт взаимодействия между task observer, control plane, OpenHands implementation stage, OpenHands review stage и runtime-слоем Kubernetes.
- [ ] 1.4 Зафиксировать расширяемый adapter contract внутри task observer для будущих источников задач без включения этих источников в MVP.

## 2. OpenHands Runtime in Kubernetes

- [ ] 2.1 Подготовить Kubernetes runtime-контур для OpenHands с sandbox workload-ами, лимитами ресурсов и сетевыми политиками.
- [ ] 2.2 Разместить OpenHands UI, implementation stage и review stage внутри Kubernetes runtime-контура.
- [ ] 2.3 Настроить Kubernetes Secrets для LLM, Jira и GitLab и подключить их к компонентам платформы.

## 3. Control Plane and Execution Stages

- [ ] 3.1 Реализовать самописный Python-сервис task observer для intake задач из Jira с нормализацией во внутренний формат.
- [ ] 3.2 Реализовать control plane для приема нормализованных задач от task observer, выбора сценария и запуска OpenHands execution.
- [ ] 3.3 Реализовать implementation stage: создание branch, изменение code/docs/config и публикация change set.
- [ ] 3.4 Реализовать review stage как вторую стадию того же execution с проверкой diff и возвратом результата в control plane.

## 4. Jira Intake and GitLab Workflow

- [ ] 4.1 Реализовать Jira intake в task observer для получения и нормализации задач.
- [ ] 4.2 Реализовать прямой GitLab workflow для OpenHands execution: branch creation, публикация изменений и создание merge request.
- [ ] 4.4 Открывать merge request только после успешного завершения review stage.
- [ ] 4.5 Подготовить extension point в task observer для будущих task source adapters без активации дополнительных интеграций в первой версии.

## 5. Validation and Pilot Rollout

- [ ] 5.1 Подготовить интеграционные тесты для task observer, control plane, Jira/GitLab адаптеров и runtime-размещения в Kubernetes.
- [ ] 5.2 Провести security/platform review для Kubernetes deployment-модели, secrets и RBAC.
- [ ] 5.3 Запустить пилот на ограниченном наборе проектов и репозиториев, собрать метрики качества и скорректировать каталог сценариев.
