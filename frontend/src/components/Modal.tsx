import React, { useState, useEffect } from 'react';
import { useApp } from '../context/AppContext';
import { useTranslation } from '../i18n';

const Modal = () => {
    const { modal, hideModal } = useApp();
    const { t } = useTranslation();
    const [selectedItems, setSelectedItems] = useState<string[]>([]);

    useEffect(() => {
        if (modal?.type === 'selection') {
            setSelectedItems([]);
        }
    }, [modal]);

    if (!modal) return null;

    const toggleSelection = (id: string) => {
        setSelectedItems(prev =>
            prev.includes(id) ? prev.filter(i => i !== id) : [...prev, id]
        );
    };

    const isSelection = modal.type === 'selection';

    return (
        <div className="fixed inset-0 z-[100] flex items-center justify-center p-4">
            <div
                className="absolute inset-0 bg-black/60 backdrop-blur-md animate-fade-in"
                onClick={hideModal}
            ></div>

            <div className={`relative w-full ${isSelection ? 'max-w-2xl' : 'max-w-md'} bg-graphite-800/80 backdrop-blur-2xl border border-white/10 rounded-[32px] p-8 shadow-[0_30px_60px_rgba(0,0,0,0.6)] animate-modal-in overflow-hidden group`}>
                <div className={`absolute -top-24 -right-24 w-48 h-48 rounded-full blur-[80px] opacity-20 pointer-events-none ${modal.type === 'danger' ? 'bg-red-500' : 'bg-neon-cyan'
                    }`}></div>

                <div className="flex items-center gap-4 mb-6">
                    <div className={`w-12 h-12 rounded-2xl flex items-center justify-center text-xl shadow-lg ${modal.type === 'danger' ? 'bg-red-500/10 text-red-500 border border-red-500/20' : 'bg-neon-cyan/10 text-neon-cyan border border-neon-cyan/20'
                        }`}>
                        {modal.type === 'danger' ? '⚠️' : isSelection ? '🔬' : 'ℹ️'}
                    </div>
                    <h3 className="text-2xl font-bold text-white tracking-tight">{modal.title}</h3>
                </div>

                <p className="text-gray-300 leading-relaxed mb-6 text-lg">
                    {modal.message}
                </p>

                {isSelection && modal.options && (
                    <div className="max-h-[40vh] overflow-y-auto mb-8 pr-2 space-y-2 scrollbar-custom">
                        {modal.options.map(opt => (
                            <label
                                key={opt.id}
                                className={`flex items-center gap-4 p-4 rounded-2xl border transition-all cursor-pointer group/item ${selectedItems.includes(opt.id)
                                        ? 'bg-neon-cyan/10 border-neon-cyan/40 shadow-lg shadow-neon-cyan/5'
                                        : 'bg-white/5 border-white/5 hover:bg-white/10 hover:border-white/10'
                                    }`}
                            >
                                <div className={`w-6 h-6 rounded-lg border-2 flex items-center justify-center transition-all ${selectedItems.includes(opt.id)
                                        ? 'bg-neon-cyan border-neon-cyan text-white'
                                        : 'border-white/20 group-hover/item:border-white/40'
                                    }`}>
                                    {selectedItems.includes(opt.id) && '✓'}
                                </div>
                                <input
                                    type="checkbox"
                                    className="hidden"
                                    checked={selectedItems.includes(opt.id)}
                                    onChange={() => toggleSelection(opt.id)}
                                />
                                <div className="flex flex-col min-w-0">
                                    <span className="font-medium text-white truncate">{opt.label}</span>
                                    <span className="text-[10px] text-gray-500 font-mono truncate">{opt.id}</span>
                                </div>
                            </label>
                        ))}
                    </div>
                )}

                <div className="flex gap-3">
                    <button
                        onClick={hideModal}
                        className="flex-1 px-6 py-4 rounded-2xl bg-white/5 hover:bg-white/10 text-white font-bold transition-all border border-white/5 active:scale-95"
                    >
                        {modal.cancelLabel || t('cancel')}
                    </button>
                    <button
                        onClick={() => { modal.onConfirm(isSelection ? selectedItems : undefined); hideModal(); }}
                        className={`flex-1 px-6 py-4 rounded-2xl font-bold text-white transition-all shadow-xl active:scale-95 ${modal.type === 'danger' ? 'bg-red-500 hover:bg-red-600 shadow-red-500/20' : 'bg-neon-cyan hover:bg-neon-cyan/80 shadow-neon-cyan/20'
                            }`}
                    >
                        {isSelection ? t('confirm') : (modal.confirmLabel || t('confirm'))}
                    </button>
                </div>
            </div>
        </div>
    );
};

export default Modal;
