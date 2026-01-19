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
  }: any) => {
    const isProcessed = site.path.endsWith("_processed");
    const displayName = site.domain || site.name;
    const percent = progress
      ? Math.min(Math.round((progress.current / progress.total) * 100), 100)
      : 0;

    return (
      <div
        style={{ animationDelay: `${index * 40}ms` }}
        className="group relative bg-graphite-800/40 backdrop-blur-xl border border-white/5 rounded-3xl p-6 hover:bg-graphite-700/60 transition-all hover:-translate-y-2 hover:shadow-[0_20px_60px_rgba(0,0,0,0.6)] animate-toast-in"
      >
        {/* Top Control Overlay */}
        <div className="absolute top-4 right-4 flex gap-1 opacity-0 group-hover:opacity-100 transition-all transform translate-y-2 group-hover:translate-y-0 z-20">
          {!isProcessed && (
            <>
              <button
                disabled={isAdapting}
                onClick={() => onAnalyze(site.path, displayName)}
                className="w-8 h-8 flex items-center justify-center bg-purple-500/10 hover:bg-purple-500 text-purple-400 hover:text-white rounded-lg transition-all border border-purple-500/20"
              >
                🔬
              </button>
              <button
                disabled={isAdapting}
                onClick={() => onAdapt(site.path, displayName)}
                className="w-8 h-8 flex items-center justify-center bg-neon-cyan/10 hover:bg-neon-cyan text-neon-cyan hover:text-white rounded-lg transition-all border border-neon-cyan/20"
              >
                🛠️
              </button>
            </>
          )}
          <button
            onClick={() => onOpenFolder(site.path)}
            className="w-8 h-8 flex items-center justify-center bg-white/5 hover:bg-white/20 rounded-lg transition-all"
          >
            📂
          </button>
          <button
            onClick={() => onDelete(site.path, displayName)}
            className="w-8 h-8 flex items-center justify-center bg-red-500/10 hover:bg-red-500 text-red-500 hover:text-white rounded-lg transition-all"
          >
            🗑️
          </button>
        </div>

        {/* Info */}
        <div className="flex items-center gap-4 mb-6">
          <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-white/5 to-white/10 flex items-center justify-center text-2xl border border-white/5 group-hover:border-neon-cyan/30 shrink-0 transition-colors">
            {site.icon ? (
              <img src={site.icon} alt="" className="w-8 h-8 object-contain" />
            ) : (
              "🌐"
            )}
          </div>
          <div className="min-w-0 flex-1">
            <h3 className="font-bold text-white text-lg truncate group-hover:text-neon-cyan transition-colors">
              {displayName}
            </h3>
            <p className="text-[10px] text-gray-500 font-mono truncate opacity-60 italic">
              {site.path}
            </p>
          </div>
        </div>

        {/* Progress Bar for Adaptation */}
        {isAdapting && (
          <div className="mb-6 animate-fade-in">
            <div className="flex justify-between text-[10px] font-mono text-neon-cyan mb-2 tracking-tighter">
              <span className="uppercase">
                {isAnalyzing
                  ? "Analyzing Structure..."
                  : "Recalibrating Paths..."}
              </span>
              <span>{percent}%</span>
            </div>
            <div className="h-1.5 w-full bg-black/40 rounded-full overflow-hidden border border-white/5">
              <div
                className="h-full bg-neon-cyan shadow-[0_0_10px_#00ffff] transition-all duration-500"
                style={{ width: `${percent}%` }}
              ></div>
            </div>
          </div>
        )}

        {/* Action Button */}
        <button
          disabled={isAdapting}
          onClick={() => (isRunning ? onStop() : onLaunch(site.path))}
          className={`w-full py-3 rounded-2xl text-sm font-black transition-all border flex items-center justify-center gap-3 ${
            isRunning
              ? "bg-red-500/10 border-red-500/30 text-red-500 hover:bg-red-500 hover:text-white shadow-lg shadow-red-500/20"
              : isProcessed
                ? "bg-green-500/10 border-green-500/30 text-green-400 hover:bg-green-500 hover:text-white shadow-lg shadow-green-500/20"
                : "bg-neon-cyan/10 border-neon-cyan/20 text-neon-cyan hover:bg-neon-cyan hover:text-white shadow-lg shadow-neon-cyan/20"
          }`}
        >
          {isRunning ? (
            <>
              <span className="animate-pulse">⏹️</span> {t("close")}
            </>
          ) : isAdapting ? (
            <>
              <span className="animate-spin">⏳</span> {t("processing")}
            </>
          ) : (
            <>
              <span className="group-hover:translate-x-1 transition-transform">
                🚀
              </span>{" "}
              {t("launch")}
            </>
          )}
        </button>

        {/* Status Badges */}
        {isRunning && (
          <div className="absolute -top-2 -left-2 px-3 py-1 rounded-lg bg-red-500 text-white font-black text-[9px] uppercase shadow-[0_0_15px_rgba(239,68,68,0.5)] flex items-center gap-1.5 z-10 border border-white/20">
            <span className="w-1.5 h-1.5 rounded-full bg-white animate-ping"></span>{" "}
            {t("status_running")}
          </div>
        )}
        {isProcessed && !isAdapting && !isRunning && (
          <div className="absolute -top-2 -left-2 px-3 py-1 rounded-lg bg-neon-cyan text-black font-black text-[9px] uppercase shadow-[0_0_15px_rgba(0,255,255,0.3)] z-10 border border-white/20">
            {t("status_adapted")}
          </div>
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
  const [adaptationProgress, setAdaptationProgress] = useState<
    Record<string, any>
  >({});

  useEffect(() => {
    const unlisten = EventsOn("adaptation:progress", (data: any) => {
      // Нормализуем путь от бэкенда, чтобы он совпал с тем, что в объекте site
      const normPath = data.path.replace(/\\/g, "/").toLowerCase();
      setAdaptationProgress((prev) => ({
        ...prev,
        [normPath]: { current: data.current, total: data.total },
      }));
    });
    return () => unlisten();
  }, []);
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
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 p-10 gap-8 overflow-y-auto">
          {sites.map((site, i) => {
            const sitePath = normalizePath(site.path);
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
