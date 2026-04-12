import { motion } from 'motion/react';

export function AuthLoadingScreen() {
  return (
    <div className="admin-console flex min-h-screen items-center justify-center bg-[var(--admin-bg)]">
      <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} className="flex flex-col items-center gap-4">
        <div className="h-12 w-12 animate-spin rounded-none border-4 border-cyan-400/20 border-t-cyan-400" />
        <p className="text-sm font-medium text-[var(--admin-text-soft)]">Initializing Pairline Console...</p>
      </motion.div>
    </div>
  );
}
