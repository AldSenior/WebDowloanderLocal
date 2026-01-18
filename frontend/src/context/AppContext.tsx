import React, { createContext, useContext, useState, ReactNode, useEffect } from 'react';
// @ts-ignore
import { EventsOn } from "../../wailsjs/runtime";

type Theme = 'graphite' | 'ocean' | 'matrix';
type Lang = 'en' | 'ru';

interface EngineSettings {
    workers: number;
    maxDepth: number;
    userAgent: string;
}

interface Toast {
    id: string;
    message: string;
    type: 'success' | 'error' | 'info' | 'warning';
}

interface ModalConfig {
    title: string;
    message: string;
    confirmLabel?: string;
    cancelLabel?: string;
    onConfirm: (selected?: string[]) => void;
    type?: 'danger' | 'info' | 'selection';
    options?: { id: string, label: string }[];
}

interface AppContextType {
    theme: Theme;
    setTheme: (theme: Theme) => void;
    lang: Lang;
    setLang: (lang: Lang) => void;
    engineSettings: EngineSettings;
    setEngineSettings: (settings: EngineSettings) => void;
    toasts: Toast[];
    addToast: (message: string, type: Toast['type']) => void;
    removeToast: (id: string) => void;
    modal: ModalConfig | null;
    showModal: (config: ModalConfig) => void;
    hideModal: () => void;

    // Persistent Download State
    isDownloading: boolean;
    setIsDownloading: (val: boolean) => void;
    downloadLogs: string[];
    setDownloadLogs: (logs: string[] | ((prev: string[]) => string[])) => void;
    clearDownloadLogs: () => void;

    // Server State
    servingPath: string | null;
    setServingPath: (path: string | null) => void;
}

const AppContext = createContext<AppContextType | undefined>(undefined);

export const AppProvider = ({ children }: { children: ReactNode }) => {
    const [theme, setThemeState] = useState<Theme>(() => {
        return (localStorage.getItem('theme') as Theme) || 'graphite';
    });

    const [lang, setLangState] = useState<Lang>(() => {
        return (localStorage.getItem('lang') as Lang) || 'ru';
    });

    const [engineSettings, setEngineSettings] = useState<EngineSettings>(() => {
        const saved = localStorage.getItem('engineSettings');
        return saved ? JSON.parse(saved) : {
            workers: 20,
            maxDepth: 15,
            userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)...'
        };
    });

    const [toasts, setToasts] = useState<Toast[]>([]);
    const [modal, setModal] = useState<ModalConfig | null>(null);

    // Download state (persists across tab switches)
    const [isDownloading, setIsDownloading] = useState(false);
    const [downloadLogs, setDownloadLogs] = useState<string[]>([]);

    // Server state
    const [servingPath, setServingPath] = useState<string | null>(null);

    useEffect(() => {
        localStorage.setItem('theme', theme);
        document.documentElement.setAttribute('data-theme', theme);
        document.body.className = `theme-${theme}`;
    }, [theme]);

    useEffect(() => {
        localStorage.setItem('lang', lang);
    }, [lang]);

    useEffect(() => {
        localStorage.setItem('engineSettings', JSON.stringify(engineSettings));
    }, [engineSettings]);

    // Listen for global download events at the provider level
    useEffect(() => {
        let logBuffer: string[] = [];
        let throttleTimer: any = null;

        const flushLogs = () => {
            if (logBuffer.length === 0) return;
            const currentBuffer = [...logBuffer];
            logBuffer = [];
            setDownloadLogs(prev => {
                const newLogs = [...prev, ...currentBuffer];
                return newLogs.slice(-500);
            });
            throttleTimer = null;
        };

        const cleanupLog = EventsOn("download:log", (msg: string) => {
            logBuffer.push(typeof msg === 'string' ? msg : JSON.stringify(msg));
            if (!throttleTimer) {
                throttleTimer = setTimeout(flushLogs, 50);
            }
        });

        const cleanupStart = EventsOn("download:start", () => {
            setIsDownloading(true);
            setDownloadLogs([]);
            logBuffer = [];
        });

        const cleanupDone = EventsOn("download:done", () => {
            setIsDownloading(false);
            flushLogs();
        });

        const cleanupServerStarted = EventsOn("server:started", (data: { path: string }) => {
            setServingPath(data.path);
        });

        const cleanupServerStopped = EventsOn("server:stopped", () => {
            setServingPath(null);
        });

        return () => {
            cleanupLog();
            cleanupStart();
            cleanupDone();
            cleanupServerStarted();
            cleanupServerStopped();
            if (throttleTimer) clearTimeout(throttleTimer);
        };
    }, []);

    const setTheme = (t: Theme) => setThemeState(t);
    const setLang = (l: Lang) => setLangState(l);

    const addToast = (message: string, type: Toast['type']) => {
        const id = Math.random().toString(36).substr(2, 9);
        setToasts((prev) => [...prev, { id, message, type }]);
        setTimeout(() => removeToast(id), 5000);
    };

    const removeToast = (id: string) => {
        setToasts((prev) => prev.filter((t) => t.id !== id));
    };

    const showModal = (config: ModalConfig) => setModal(config);
    const hideModal = () => setModal(null);

    const clearDownloadLogs = () => setDownloadLogs([]);

    return (
        <AppContext.Provider value={{
            theme, setTheme,
            lang, setLang,
            engineSettings, setEngineSettings,
            toasts, addToast, removeToast,
            modal, showModal, hideModal,
            isDownloading, setIsDownloading,
            downloadLogs, setDownloadLogs,
            clearDownloadLogs,
            servingPath, setServingPath
        }}>
            {children}
        </AppContext.Provider>
    );
};

export const useApp = () => {
    const context = useContext(AppContext);
    if (!context) throw new Error('useApp must be used within AppProvider');
    return context;
};
