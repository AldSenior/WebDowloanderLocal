import React, { useEffect, useState, useMemo } from 'react';
// @ts-ignore
import { GetDownloads, OpenFolder, LaunchSite, StopServer, DeleteSite, AdaptPaths, AnalyzeScripts } from "../../wailsjs/go/main/App";
// @ts-ignore
import { EventsOn } from "../../wailsjs/runtime";
import { useTranslation } from '../i18n';
import { useApp } from '../context/AppContext';

interface Site {
    name: string;
    path: string;
    icon?: string;
    domain?: string;
    entryPath?: string;
}

interface Progress {
    current: number;
    total: number;
    completed?: boolean;
}

const LibraryGrid = () => {
    const { t } = useTranslation();
    const { addToast, showModal, servingPath } = useApp();
    const [sites, setSites] = useState<Site[]>([]);
    const [loading, setLoading] = useState(true);
    const [progressMap, setProgressMap] = useState<Record<string, Progress>>({});
    const [isAdaptingMap, setIsAdaptingMap] = useState<Record<string, boolean>>({});
    const [isAnalyzingMap, setIsAnalyzingMap] = useState<Record<string, boolean>>({});

    const fetchSites = async () => {
        setLoading(true);
        try {
            const res = await GetDownloads();
            setSites(res || []);
        } catch (e) {
            console.error(e);
            addToast(t('fetch_failed'), "error");
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        fetchSites();

        const cleanupRefresh = EventsOn("library:refresh", () => {
            fetchSites();
        });

        const cleanupProgress = EventsOn("adaptation:progress", (data: any) => {
            const normalizedPath = data.path.replace(/\\/g, '/');
            setIsAnalyzingMap(prev => ({ ...prev, [normalizedPath]: false }));
            setProgressMap(prev => ({
                ...prev,
                [normalizedPath]: {
                    current: data.current,
                    total: data.total,
                    completed: data.current >= data.total
                }
            }));
        });

        const cleanupAnalyzing = EventsOn("adaptation:analyzing", (path: string) => {
            const normalizedPath = path.replace(/\\/g, '/');
            setIsAnalyzingMap(prev => ({ ...prev, [normalizedPath]: true }));
        });

        const cleanupStart = EventsOn("adapting:start", (path: string) => {
            const normalizedPath = path.replace(/\\/g, '/');
            setIsAdaptingMap(prev => ({ ...prev, [normalizedPath]: true }));
        });

        const cleanupDone = EventsOn("adapting:done", (path: string) => {
            const normalizedPath = path.replace(/\\/g, '/');
            setIsAdaptingMap(prev => ({ ...prev, [normalizedPath]: false }));
            setIsAnalyzingMap(prev => ({ ...prev, [normalizedPath]: false }));

            // Give it a moment to show 100% before removing
            setTimeout(() => {
                setProgressMap(prev => {
                    const next = { ...prev };
                    delete next[normalizedPath];
                    return next;
                });
            }, 1000);
        });

        return () => {
            cleanupRefresh();
            cleanupProgress();
            cleanupAnalyzing();
            cleanupStart();
            cleanupDone();
        };
    }, []);

    const handleOpenFolder = (path: string) => {
        OpenFolder(path);
        addToast(t('opening_folder'), 'info');
    };

    const handleLaunch = async (path: string) => {
        addToast(t('launching'), 'success');
        try {
            await LaunchSite(path);
        } catch (err) {
            console.error("Launch failed:", err);
            addToast("Launch failed", "error");
        }
    };

    const handleStop = async () => {
        try {
            await StopServer();
            addToast(t('stopped'), 'info');
        } catch (err) {
            console.error("Stop failed:", err);
            addToast("Stop failed", "error");
        }
    };

    const handleAdaptTrigger = (path: string, name: string) => {
        const normalizedPath = path.replace(/\\/g, '/');
        if (isAdaptingMap[normalizedPath]) return;

        showModal({
            title: t('adapt_action'),
            message: `${t('adapt_info')} (${name})`,
            type: 'info',
            confirmLabel: t('confirm'),
            cancelLabel: t('cancel'),
            onConfirm: () => AdaptPaths(path, [])
        });
    };

    const handleAnalyze = async (path: string, name: string) => {
        const normalizedPath = path.replace(/\\/g, '/');
        if (isAdaptingMap[normalizedPath]) return;

        addToast("Analyzing...", 'info');
        try {
            const scripts = await AnalyzeScripts(path);
            if (scripts && scripts.length > 0) {
                showModal({
                    title: `🔬 ${name}`,
                    message: "Select scripts/trackers to remove:",
                    type: 'selection',
                    options: scripts.map((s: string) => ({ id: s, label: s.split('/').pop() || s })),
                    confirmLabel: "Apply & Adapt",
                    onConfirm: (selected) => {
                        if (selected) {
                            AdaptPaths(path, selected);
                        }
                    }
                });
            } else {
                addToast("No external scripts found.", 'info');
            }
        } catch (err) {
            addToast("Analysis failed", 'error');
        }
    };

    const handleDelete = (path: string, name: string) => {
        showModal({
            title: t('delete'),
            message: `${t('delete_confirm')} (${name})`,
            type: 'danger',
            confirmLabel: t('delete'),
            cancelLabel: t('cancel'),
            onConfirm: async () => {
                try {
                    const res = await DeleteSite(path);
                    if (res === "Deleted") {
                        addToast(t('deleted'), 'success');
                        fetchSites();
                    } else {
                        addToast(res, 'error');
                    }
                } catch (e) {
                    addToast(String(e), 'error');
                }
            }
        });
    };

    return (
        <div className="h-full flex flex-col pt-2">
            <div className="flex items-center justify-between mb-8">
                <div>
                    <h2 className="text-3xl font-extrabold bg-clip-text text-transparent bg-gradient-to-r from-white via-white to-white/40 tracking-tight">
                        {t('library')}
                    </h2>
                    <p className="text-gray-500 text-sm mt-1">{sites.length} {t('library').toLowerCase()}</p>
                </div>
                <button
                    onClick={fetchSites}
                    className="group w-10 h-10 flex items-center justify-center bg-white/5 hover:bg-neon-cyan/20 rounded-xl transition-all border border-white/5 hover:border-neon-cyan/50 shadow-lg"
                    title={t('refresh')}
                >
                    <span className="group-hover:rotate-180 transition-transform duration-500">🔄</span>
                </button>
            </div>

            {loading ? (
                <div className="flex-1 flex items-center justify-center">
                    <div className="flex flex-col items-center gap-4">
                        <div className="w-12 h-12 border-2 border-neon-cyan/20 border-t-neon-cyan rounded-full animate-spin"></div>
                        <span className="text-gray-500 animate-pulse font-mono text-xs uppercase tracking-widest italic">{t('processing')}</span>
                    </div>
                </div>
            ) : sites.length === 0 ? (
                <div className="flex-1 flex flex-col items-center justify-center text-gray-700">
                    <div className="w-20 h-20 rounded-3xl bg-white/5 flex items-center justify-center text-4xl mb-6 grayscale opacity-20">📂</div>
                    <p className="font-medium">{t('no_sites')}</p>
                </div>
            ) : (
                <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-8 overflow-y-auto pb-32 pt-4 scrollbar-custom px-6 -mx-6">
                    {sites.map((site, i) => {
                        const normalizedPath = site.path.replace(/\\/g, '/');
                        const isProcessed = site.path.endsWith("_processed");
                        const displayName = site.domain || site.name;
                        const progress = progressMap[normalizedPath];
                        const percent = progress ? Math.min(Math.round((progress.current / progress.total) * 100), 100) : 0;
                        const isAdapting = isAdaptingMap[normalizedPath];
                        const isAnalyzing = isAnalyzingMap[normalizedPath];
                        const isRunning = servingPath === normalizedPath;

                        return (
                            <div
                                key={i}
                                style={{ animationDelay: `${i * 40}ms` }}
                                className="group relative bg-graphite-800/40 backdrop-blur-xl border border-white/5 rounded-3xl p-6 hover:bg-graphite-700/60 transition-all hover:-translate-y-2 hover:shadow-[0_20px_60px_rgba(0,0,0,0.6)] hover:border-white/10 animate-toast-in overflow-visible"
                            >
                                <div className="absolute top-4 right-4 flex gap-1 opacity-0 group-hover:opacity-100 transition-all transform translate-y-2 group-hover:translate-y-0 z-20">
                                    {!isProcessed && (
                                        <>
                                            <button
                                                disabled={isAdapting}
                                                onClick={() => handleAnalyze(site.path, displayName)}
                                                className="w-8 h-8 flex items-center justify-center bg-purple-500/10 hover:bg-purple-500 text-purple-500 hover:text-white rounded-lg text-sm transition-all shadow-lg shadow-purple-500/10 border border-purple-500/20 disabled:opacity-50 disabled:cursor-not-allowed"
                                                title="Analyze Scripts"
                                            >
                                                🔬
                                            </button>
                                            <button
                                                disabled={isAdapting}
                                                onClick={() => handleAdaptTrigger(site.path, displayName)}
                                                className="w-8 h-8 flex items-center justify-center bg-neon-cyan/10 hover:bg-neon-cyan text-neon-cyan hover:text-white rounded-lg text-sm transition-all shadow-lg shadow-neon-cyan/10 border border-neon-cyan/20 disabled:opacity-50 disabled:cursor-not-allowed"
                                                title={t('adapt_action')}
                                            >
                                                🛠️
                                            </button>
                                        </>
                                    )}
                                    <button
                                        onClick={() => handleOpenFolder(site.path)}
                                        className="w-8 h-8 flex items-center justify-center bg-white/5 hover:bg-white/20 rounded-lg text-sm transition-all"
                                        title={t('open_folder')}
                                    >
                                        📂
                                    </button>
                                    <button
                                        onClick={() => handleDelete(site.path, displayName)}
                                        className="w-8 h-8 flex items-center justify-center bg-red-500/10 hover:bg-red-500 text-red-500 hover:text-white rounded-lg text-sm transition-all"
                                        title={t('delete')}
                                    >
                                        🗑️
                                    </button>
                                </div>

                                <div className="flex items-center gap-4 mb-6 relative">
                                    <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-white/5 to-white/10 flex items-center justify-center text-2xl shadow-inner border border-white/5 group-hover:scale-110 transition-transform duration-500 group-hover:shadow-[0_0_20px_rgba(14,165,233,0.2)] group-hover:border-neon-cyan/30 overflow-hidden shrink-0">
                                        {site.icon ? (
                                            <img src={site.icon} alt={displayName} className="w-8 h-8 object-contain drop-shadow-lg" />
                                        ) : (
                                            "🌐"
                                        )}
                                    </div>
                                    <div className="min-w-0 flex-1">
                                        <h3 className="font-bold text-white text-lg truncate pr-2" title={displayName}>{displayName}</h3>
                                        <p className="text-[10px] text-gray-500 font-mono truncate opacity-60 group-hover:opacity-100">{normalizedPath}</p>
                                    </div>
                                </div>

                                {/* Progress Bar (Visible during adaptation) */}
                                {isAdapting && (
                                    <div className="mb-6 animate-pulse-subtle">
                                        <div className="flex justify-between text-[10px] font-mono text-neon-cyan mb-1.5 px-0.5">
                                            <span className="animate-pulse">{isAnalyzing ? t('analyzing').toUpperCase() : 'ADAPTING...'}</span>
                                            <span>{percent}%</span>
                                        </div>
                                        <div className="h-2 w-full bg-black/40 rounded-full overflow-hidden border border-white/5 shadow-inner">
                                            <div
                                                className="h-full bg-gradient-to-r from-neon-cyan via-blue-400 to-blue-600 transition-all duration-500 ease-out shadow-[0_0_15px_rgba(14,165,233,0.6)]"
                                                style={{ width: `${percent}%` }}
                                            ></div>
                                        </div>
                                        {!isAnalyzing && (
                                            <div className="text-[9px] text-gray-500 mt-1 font-mono flex justify-between italic uppercase tracking-tighter">
                                                <span>{progress?.current ?? 0} files</span>
                                                <span>of {progress?.total ?? '...'}</span>
                                            </div>
                                        )}
                                    </div>
                                )}

                                <div className="mt-auto">
                                    <button
                                        disabled={isAdapting}
                                        onClick={() => isRunning ? handleStop() : handleLaunch(site.path)}
                                        className={`w-full py-3 rounded-2xl text-sm font-bold transition-all border flex items-center justify-center gap-2 group/btn shadow-lg disabled:opacity-50 disabled:cursor-not-allowed ${isRunning
                                            ? 'bg-red-500/10 border-red-500/30 text-red-500 hover:bg-red-500 hover:text-white hover:border-red-500'
                                            : isProcessed
                                                ? 'bg-neon-green/10 border-neon-green/30 text-neon-green hover:bg-neon-green hover:text-white hover:border-neon-green shadow-neon-green/5'
                                                : 'bg-neon-cyan/10 border-neon-cyan/20 text-neon-cyan hover:bg-neon-cyan hover:text-white hover:border-neon-cyan shadow-neon-cyan/5'
                                            }`}
                                    >
                                        <span>{isAdapting ? '⏳' : isRunning ? '⏹️' : '🚀'}</span> {isAdapting ? t('processing') : isRunning ? t('close') : t('launch')}
                                    </button>
                                </div>

                                {isRunning ? (
                                    <div className="absolute -top-1 -left-1 px-3 py-1 rounded-full bg-red-500 text-white font-black text-[9px] uppercase tracking-tighter shadow-[0_0_15px_rgba(239,68,68,0.4)] z-10 border border-white/10 flex items-center gap-1">
                                        <span className="w-1.5 h-1.5 rounded-full bg-white animate-pulse"></span>
                                        {t('status_running')}
                                    </div>
                                ) : isProcessed && !isAdapting && (
                                    <div className="absolute -top-1 -left-1 px-3 py-1 rounded-full bg-neon-green text-black font-black text-[9px] uppercase tracking-tighter shadow-[0_0_15px_rgba(34,197,94,0.4)] z-10 border border-white/10">
                                        {t('status_adapted')}
                                    </div>
                                )}
                            </div>
                        );
                    })}
                </div>
            )}
        </div>
    );
};

export default LibraryGrid;
