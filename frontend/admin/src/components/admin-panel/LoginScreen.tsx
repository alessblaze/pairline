import type React from 'react';
import { motion } from 'motion/react';
import { Eye, EyeOff, Shield, Users } from 'lucide-react';
import { inputClass, surfaceCardClass } from './styles';

interface LoginScreenProps {
  username: string;
  password: string;
  showPassword: boolean;
  onUsernameChange: (value: string) => void;
  onPasswordChange: (value: string) => void;
  onTogglePassword: () => void;
  onSubmit: (event: React.FormEvent) => void;
}

export function LoginScreen({
  username,
  password,
  showPassword,
  onUsernameChange,
  onPasswordChange,
  onTogglePassword,
  onSubmit,
}: LoginScreenProps) {
  return (
    <div className="admin-console relative flex min-h-screen items-center justify-center overflow-hidden bg-[var(--admin-bg)] transition-colors duration-300">
      <div className="absolute inset-0 z-0">
        <div className="absolute top-[-10%] left-[-10%] h-[40%] w-[40%] rounded-full bg-cyan-500/10 blur-[120px]" />
        <div className="absolute bottom-[-10%] right-[-10%] h-[40%] w-[40%] rounded-full bg-rose-500/10 blur-[120px]" />
      </div>

      <motion.div initial={{ opacity: 0, y: 20 }} animate={{ opacity: 1, y: 0 }} className="relative z-10 w-full max-w-md px-6">
        <div className="mb-8 text-center">
          <div className="mx-auto mb-6 flex h-16 w-16 items-center justify-center rounded-none bg-[var(--admin-text)] text-[var(--admin-bg)] shadow-[0_0_30px_rgba(255,255,255,0.2)]">
            <Shield size={32} />
          </div>
          <h1 className="text-4xl font-bold tracking-tight text-[var(--admin-text)]">Pairline</h1>
          <p className="mt-2 text-[var(--admin-text-soft)]">Moderation & Safety Console</p>
        </div>

        <div className={`${surfaceCardClass} p-8`}>
          <form onSubmit={onSubmit} className="space-y-6">
            <div>
              <label className="mb-2 block text-sm font-medium text-[var(--admin-text)]">Admin Username</label>
              <div className="relative">
                <Users className="absolute top-3.5 left-4 text-[var(--admin-text-muted)]" size={18} />
                <input
                  type="text"
                  value={username}
                  onChange={(event) => onUsernameChange(event.target.value.trim())}
                  className={`${inputClass} pl-12`}
                  placeholder="Enter username"
                  required
                />
              </div>
            </div>

            <div>
              <label className="mb-2 block text-sm font-medium text-[var(--admin-text)]">Security Key</label>
              <div className="relative">
                <Shield className="absolute top-3.5 left-4 text-[var(--admin-text-muted)]" size={18} />
                <input
                  type={showPassword ? 'text' : 'password'}
                  value={password}
                  onChange={(event) => onPasswordChange(event.target.value)}
                  className={`${inputClass} px-12`}
                  placeholder="Enter password"
                  required
                />
                <button
                  type="button"
                  onClick={onTogglePassword}
                  className="absolute top-3 right-4 text-[var(--admin-text-muted)] hover:text-[var(--admin-text)]"
                >
                  {showPassword ? <EyeOff size={18} /> : <Eye size={18} />}
                </button>
              </div>
            </div>

            <button
              type="submit"
              className="group relative w-full overflow-hidden rounded-none bg-[var(--admin-text)] py-3.5 text-sm font-bold text-[var(--admin-bg)] transition-all hover:scale-[1.02] active:scale-[0.98]"
            >
              <div className="absolute inset-0 bg-gradient-to-r from-cyan-400/0 via-cyan-400/20 to-cyan-400/0 opacity-0 transition-opacity group-hover:opacity-100" />
              Sign In to Console
            </button>
          </form>
        </div>

        <p className="mt-8 text-center text-xs text-[var(--admin-text-muted)]">Authorized personnel only. All actions are logged.</p>
      </motion.div>
    </div>
  );
}
