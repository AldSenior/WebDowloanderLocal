import { useApp } from './context/AppContext';

export const i18n = {
    en: {
        download: "Download",
        library: "Library",
        server: "Server",
        settings: "Settings",
        new_download: "New Download",
        url_placeholder: "https://example.com",
        start: "Start",
        processing: "Processing...",
        waiting: "Waiting for commands...",
        terminal: "TERMINAL",
        worker_pool: "worker-pool",
        version: "Version",
        open_folder: "Open Folder",
        launch: "Launch",
        refresh: "Refresh",
        no_sites: "No sites downloaded yet.",
        port: "PORT",
        directory: "DIRECTORY",
        start_server: "START SERVER",
        stop_server: "STOP SERVER",
        server_logs: "SERVER LOGS",
        appearance: "Appearance",
        engine_config: "Engine Configuration",
        workers: "Concurrent Workers",
        max_depth: "Max Depth",
        user_agent: "User Agent",
        language: "Language",
        launching: "Launching site...",
        opening_folder: "Opening folder...",
        theme: "Theme",
        stopped: "Stopped",
        error: "Error",
        started_at: "Started at",
        fetch_failed: "Failed to fetch sites",
        processor: "Processor",
        adapt_action: "Adapt paths",
        status_adapted: "Processed",
        adapt_info: "Process site for local offline viewing? (relative paths transformation)",
        delete: "Delete",
        delete_confirm: "Are you sure you want to delete this site?",
        deleted: "Site deleted successfully",
        cancel: "Cancel",
        confirm: "Confirm",
        system: "System"
    },
    ru: {
        download: "Загрузка",
        library: "Библиотека",
        server: "Сервер",
        settings: "Настройки",
        new_download: "Новая загрузка",
        url_placeholder: "https://example.com",
        start: "Запуск",
        processing: "Загрузка...",
        waiting: "Ожидание задач...",
        terminal: "ТЕРМИНАЛ",
        worker_pool: "поток-пул",
        version: "Версия",
        open_folder: "Открыть папку",
        launch: "Запустить",
        refresh: "Обновить",
        no_sites: "Сайты еще не загружены.",
        port: "ПОРТ",
        directory: "ДИРЕКТОРИЯ",
        start_server: "ЗАПУСТИТЬ СЕРВЕР",
        stop_server: "ОСТАНОВИТЬ СЕРВЕР",
        server_logs: "ЛОГИ СЕРВЕРА",
        appearance: "Внешний вид",
        engine_config: "Конфигурация движка",
        workers: "Параллельные вокеры",
        max_depth: "Макс. глубина",
        user_agent: "User Agent",
        language: "Язык",
        launching: "Запуск сайта...",
        opening_folder: "Открытие папки...",
        theme: "Тема",
        stopped: "Остановлен",
        error: "Ошибка",
        started_at: "Запущен на",
        fetch_failed: "Ошибка загрузки списка сайтов",
        processor: "Процессор",
        adapt_action: "Адаптировать",
        status_adapted: "Обработан",
        adapt_info: "Подготовить сайт для локального просмотра? (замена путей на относительные)",
        delete: "Удалить",
        delete_confirm: "Вы уверены, что хотите удалить этот сайт?",
        deleted: "Сайт успешно удален",
        cancel: "Отмена",
        confirm: "Да",
        system: "Система"
    }
};

export type Lang = keyof typeof i18n;

export const useTranslation = () => {
    const { lang, setLang } = useApp();

    const t = (key: keyof typeof i18n.en) => {
        return i18n[lang][key] || i18n.en[key];
    };

    return { t, lang, setLang };
};
