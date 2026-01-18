import React, {
  useState,
  useEffect,
  useMemo,
  useCallback,
  useRef,
} from "react";
// @ts-ignore
import {
  GetDownloads,
  OpenFolder,
  LaunchSite,
  StopServer,
  AdaptPaths,
  DeleteSite,
  AnalyzeScripts,
} from "../../wailsjs/go/main/App";
// @ts-ignore
import { EventsOn } from "../../wailsjs/runtime";
import { useTranslation } from "../i18n";
import { useApp } from "../context/AppContext";

interface Site {
  name: string;
  path: string;
  domain?: string;
  icon?: string;
  entryPath?: string;
}

interface Progress {
  current: number;
  total: number;
  completed: boolean;
}

// Вспомогательная функция для стандартизации путей
const normalizePath = (p: string | undefined | null) => {
  if (!p) return "";
  return p.replace(/\\/g, "/").toLowerCase().trim();
};

const SiteCard = React.memo(
  ({
    site,
    index,
    progress,
    isAdapting,
    isAnalyzing,
    isRunning,
    t,
    onLaunch,
    onStop,
    onAnalyze,
    onAdapt,
    onOpenFolder,
    onDelete,
  }: {
    site: Site;
    index: number;
    progress: Progress | undefined;
    isAdapting: boolean;
    isAnalyzing: boolean;
    isRunning: boolean;
    t: any;
    onLaunch: (p: string) => void;
    onStop: () => void;
    onAnalyze: (p: string, n: string) => void;
    onAdapt: (p: string, n: string) => void;
    onOpenFolder: (p: string) => void;
    onDelete: (p: string, n: string) => void;
  }) => {
    const isProcessed = site.path.endsWith("_processed");
    const displayName = site.domain || site.name;
    const percent = progress
      ? Math.min(Math.round((progress.current / progress.total) * 100), 100)
      : 0;

    return (
      <div
        style={{ animationDelay: `${index * 40}ms` }}
        className="group relative bg-graphite-800/40 backdrop-blur-xl border border-white/5 rounded-3xl p-6 hover:bg-graphite-700/60 transition-all hover:-translate-y-2 hover:shadow-[0_20px_60px_rgba(0,0,0,0.6)] hover:border-white/10 animate-toast-in overflow-visible"
      >
        {/* Кнопки управления в углу */}
        <div className="absolute top-4 right-4 flex gap-1 opacity-0 group-hover:opacity-100 transition-all transform translate-y-2 group-hover:translate-y-0 z-20">
          {!isProcessed && (
            <>
              <button
                disabled={isAdapting}
                onClick={() => onAnalyze(site.path, displayName)}
                className="w-8 h-8 flex items-center justify-center bg-purple-500/10 hover:bg-purple-500 text-purple-500 hover:text-white rounded-lg text-sm transition-all border border-purple-500/20 disabled:opacity-50"
              >
                🔬
              </button>
              <button
                disabled={isAdapting}
                onClick={() => onAdapt(site.path, displayName)}
                className="w-8 h-8 flex items-center justify-center bg-neon-cyan/10 hover:bg-neon-cyan text-neon-cyan hover:text-white rounded-lg text-sm transition-all border border-neon-cyan/20 disabled:opacity-50"
              >
                🛠️
              </button>
            </>
          )}
          <button
            onClick={() => onOpenFolder(site.path)}
            className="w-8 h-8 flex items-center justify-center bg-white/5 hover:bg-white/20 rounded-lg text-sm transition-all"
          >
            📂
          </button>
          <button
            onClick={() => onDelete(site.path, displayName)}
            className="w-8 h-8 flex items-center justify-center bg-red-500/10 hover:bg-red-500 text-red-500 hover:text-white rounded-lg text-sm transition-all"
          >
            🗑️
          </button>
        </div>

        {/* Инфо о сайте */}
        <div className="flex items-center gap-4 mb-6 relative">
          <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-white/5 to-white/10 flex items-center justify-center text-2xl border border-white/5 group-hover:border-neon-cyan/30 shrink-0">
            {site.icon ? (
              <img src={site.icon} alt="" className="w-8 h-8 object-contain" />
            ) : (
              "🌐"
            )}
          </div>
          <div className="min-w-0 flex-1">
            <h3 className="font-bold text-white text-lg truncate">
              {displayName}
            </h3>
            <p className="text-[10px] text-gray-500 font-mono truncate opacity-60">
              {site.path}
            </p>
          </div>
        </div>

        {/* Прогресс адаптации */}
        {isAdapting && (
          <div className="mb-6">
            <div className="flex justify-between text-[10px] font-mono text-neon-cyan mb-1.5">
              <span>
                {isAnalyzing ? t("analyzing").toUpperCase() : "ADAPTING..."}
              </span>
              <span>{percent}%</span>
            </div>
            <div className="h-2 w-full bg-black/40 rounded-full overflow-hidden border border-white/5">
              <div
                className="h-full bg-neon-cyan transition-all duration-500"
                style={{ width: `${percent}%` }}
              ></div>
            </div>
          </div>
        )}

        {/* ГЛАВНАЯ КНОПКА (Запустить / Закрыть) */}
        <div className="mt-auto">
          <button
            disabled={isAdapting}
            onClick={() => (isRunning ? onStop() : onLaunch(site.path))}
            className={`w-full py-3 rounded-2xl text-sm font-bold transition-all border flex items-center justify-center gap-2 ${
              isRunning
                ? "bg-red-500/10 border-red-500/30 text-red-500 hover:bg-red-500 hover:text-white"
                : isProcessed
                  ? "bg-neon-green/10 border-neon-green/30 text-neon-green hover:bg-neon-green hover:text-white"
                  : "bg-neon-cyan/10 border-neon-cyan/20 text-neon-cyan hover:bg-neon-cyan hover:text-white"
            }`}
          >
            <span>{isRunning ? "⏹️" : isAdapting ? "⏳" : "🚀"}</span>
            {isAdapting
              ? t("processing")
              : isRunning
                ? t("close")
                : t("launch")}
          </button>
        </div>

        {/* Статусы-бейджи */}
        {isRunning ? (
          <div className="absolute -top-1 -left-1 px-3 py-1 rounded-full bg-red-500 text-white font-black text-[9px] uppercase z-10 flex items-center gap-1 shadow-lg">
            <span className="w-1.5 h-1.5 rounded-full bg-white animate-pulse"></span>
            {t("status_running")}
          </div>
        ) : (
          isProcessed &&
          !isAdapting && (
            <div className="absolute -top-1 -left-1 px-3 py-1 rounded-full bg-neon-green text-black font-black text-[9px] uppercase z-10 shadow-lg">
              {t("status_adapted")}
            </div>
          )
        )}
      </div>
    );
  },
);

const LibraryGrid = () => {
  const { t } = useTranslation();
  const { addToast, showModal, servingPath } = useApp();
  const [sites, setSites] = useState<Site[]>([]);
  const [loading, setLoading] = useState(true);
  const [progressMap, setProgressMap] = useState<Record<string, Progress>>({});
  const [isAdaptingMap, setIsAdaptingMap] = useState<Record<string, boolean>>(
    {},
  );
  const [isAnalyzingMap, setIsAnalyzingMap] = useState<Record<string, boolean>>(
    {},
  );

  const fetchSitesRef = useRef<(sl?: boolean) => Promise<void>>();

  // Нормализуем текущий запущенный путь для сравнения
  const normalizedServingPath = useMemo(
    () => normalizePath(servingPath),
    [servingPath],
  );

  const fetchSites = useCallback(
    async (showLoading = true) => {
      if (showLoading) setLoading(true);
      try {
        const res = await GetDownloads();
        setSites(res || []);
      } catch (e) {
        addToast(t("fetch_failed"), "error");
      } finally {
        if (showLoading) setLoading(false);
      }
    },
    [t, addToast],
  );

  useEffect(() => {
    fetchSitesRef.current = fetchSites;
    fetchSites();

    const cleanupRefresh = EventsOn("library:refresh", () =>
      fetchSitesRef.current?.(false),
    );
    const cleanupProgress = EventsOn("adaptation:progress", (data: any) => {
      const p = normalizePath(data.path);
      setIsAnalyzingMap((prev) => ({ ...prev, [p]: false }));
      setProgressMap((prev) => ({
        ...prev,
        [p]: {
          current: data.current,
          total: data.total,
          completed: data.current >= data.total,
        },
      }));
    });
    const cleanupAnalyzing = EventsOn("adaptation:analyzing", (p: string) =>
      setIsAnalyzingMap((prev) => ({ ...prev, [normalizePath(p)]: true })),
    );
    const cleanupStart = EventsOn("adapting:start", (p: string) =>
      setIsAdaptingMap((prev) => ({ ...prev, [normalizePath(p)]: true })),
    );
    const cleanupDone = EventsOn("adapting:done", (p: string) => {
      const path = normalizePath(p);
      setIsAdaptingMap((prev) => ({ ...prev, [path]: false }));
      setTimeout(
        () =>
          setProgressMap((prev) => {
            const n = { ...prev };
            delete n[path];
            return n;
          }),
        1000,
      );
      fetchSitesRef.current?.(false);
    });

    return () => {
      cleanupRefresh();
      cleanupProgress();
      cleanupAnalyzing();
      cleanupStart();
      cleanupDone();
    };
  }, [fetchSites]);

  const handleOpenFolder = useCallback((p: string) => OpenFolder(p), []);
  const handleLaunch = useCallback(
    async (p: string) => {
      try {
        await LaunchSite(p);
        addToast(t("launching"), "success");
      } catch {
        addToast("Error", "error");
      }
    },
    [t, addToast],
  );
  const handleStop = useCallback(async () => {
    try {
      await StopServer();
      addToast(t("stopped"), "info");
    } catch {
      addToast("Error", "error");
    }
  }, [t, addToast]);

  const handleAdaptTrigger = useCallback(
    (path: string, name: string) => {
      showModal({
        title: t("adapt_action"),
        message: `${t("adapt_info")} (${name})`,
        type: "info",
        confirmLabel: t("confirm"),
        onConfirm: () => AdaptPaths(path, []),
      });
    },
    [t, showModal],
  );

  const handleAnalyze = useCallback(
    async (path: string, name: string) => {
      addToast("Analyzing...", "info");
      try {
        const scripts = await AnalyzeScripts(path);
        showModal({
          title: `🔬 ${name}`,
          message: "Select scripts to remove:",
          type: "selection",
          options: scripts?.map((s: string) => ({
            id: s,
            label: s.split("/").pop() || s,
          })),
          confirmLabel: "Apply",
          onConfirm: (selected) => {
            if (selected) AdaptPaths(path, selected);
          },
        });
      } catch {
        addToast("Failed", "error");
      }
    },
    [addToast, showModal],
  );

  const handleDelete = useCallback(
    (path: string, name: string) => {
      showModal({
        title: t("delete"),
        message: name,
        type: "danger",
        confirmLabel: t("delete"),
        onConfirm: async () => {
          if ((await DeleteSite(path)) === "Deleted") fetchSites(false);
        },
      });
    },
    [t, showModal, fetchSites],
  );

  return (
    <div className="h-full flex flex-col pt-2">
      <div className="flex items-center justify-between mb-8">
        <h2 className="text-3xl font-extrabold text-white">{t("library")}</h2>
        <button
          onClick={() => fetchSites()}
          className="p-2 bg-white/5 rounded-xl hover:bg-neon-cyan/20"
        >
          🔄
        </button>
      </div>

      {loading ? (
        <div className="flex-1 flex items-center justify-center">
          <div className="w-10 h-10 border-2 border-t-neon-cyan rounded-full animate-spin"></div>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-8 overflow-y-auto pb-20">
          {sites.map((site, i) => {
            const sitePath = normalizePath(site.path);
            // КЛЮЧЕВОЕ ИСПРАВЛЕНИЕ: Проверяем, начинается ли путь сервера с пути сайта или наоборот
            const isRunning =
              normalizedServingPath !== "" &&
              (normalizedServingPath.includes(sitePath) ||
                sitePath.includes(normalizedServingPath));

            return (
              <SiteCard
                key={site.path}
                site={site}
                index={i}
                progress={progressMap[sitePath]}
                isAdapting={!!isAdaptingMap[sitePath]}
                isAnalyzing={!!isAnalyzingMap[sitePath]}
                isRunning={isRunning}
                t={t}
                onLaunch={handleLaunch}
                onStop={handleStop}
                onAnalyze={handleAnalyze}
                onAdapt={handleAdaptTrigger}
                onOpenFolder={handleOpenFolder}
                onDelete={handleDelete}
              />
            );
          })}
        </div>
      )}
    </div>
  );
};

export default LibraryGrid;
