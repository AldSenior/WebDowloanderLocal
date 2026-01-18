import React from 'react';
import { useApp } from '../context/AppContext';

const ToastContainer = React.memo(() => {
    const { toasts, removeToast } = useApp();

    return (
        <div className="fixed bottom-6 right-6 z-50 flex flex-col gap-3 pointer-events-none">
            {toasts.map((toast, index) => (
                <div
                    key={toast.id}
                    className={`pointer-events-auto flex items-center gap-4 px-5 py-4 rounded-2xl border backdrop-blur-2xl shadow-[0_20px_40px_rgba(0,0,0,0.4)] transition-all animate-toast-in ${toast.type === 'success' ? 'bg-green-500/10 border-green-500/30 text-green-400' :
                        toast.type === 'error' ? 'bg-red-500/10 border-red-500/30 text-red-400' :
                            toast.type === 'warning' ? 'bg-yellow-500/10 border-yellow-500/30 text-yellow-400' :
                                'bg-neon-cyan/10 border-neon-cyan/30 text-neon-cyan'
                        }`}
                    style={{ animationDelay: `${index * 50}ms` }}
                >
                    <div className="flex items-center justify-center w-8 h-8 rounded-xl bg-white/5 border border-white/5">
                        {toast.type === 'success' ? '✓' :
                            toast.type === 'error' ? '!' :
                                toast.type === 'warning' ? '⚠' : 'ℹ'}
                    </div>

                    <div className="flex flex-col">
                        <span className="font-bold text-xs uppercase tracking-widest opacity-50 mb-0.5">
                            {toast.type || 'info'}
                        </span>
                        <span className="font-medium text-sm text-white/90">{toast.message}</span>
                    </div>

                    <button
                        onClick={() => removeToast(toast.id)}
                        className="ml-4 w-6 h-6 flex items-center justify-center rounded-lg hover:bg-white/10 transition-colors text-white/40 hover:text-white"
                    >
                        ✕
                    </button>

                    {/* Progress bar for auto-close hint */}
                    <div className={`absolute bottom-0 left-4 right-4 h-0.5 rounded-full overflow-hidden bg-white/5`}>
                        <div className={`h-full animate-toast-progress ${toast.type === 'success' ? 'bg-green-500/50' :
                            toast.type === 'error' ? 'bg-red-500/50' :
                                toast.type === 'warning' ? 'bg-yellow-500/50' : 'bg-neon-cyan/50'
                            }`}></div>
                    </div>
                </div>
            ))}
        </div>
    );
});

export default ToastContainer;
