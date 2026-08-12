import type { ReactNode } from 'react';
import { Pressable, StyleSheet, Text, View } from 'react-native';

import type { UserTab } from '../types';

export const colors = {
  ink: '#20383D',
  muted: '#66797B',
  faint: '#8B9A9B',
  canvas: '#F6F7F3',
  surface: '#FFFFFF',
  border: '#E4E9E5',
  teal: '#087A69',
  tealDark: '#075B52',
  tealWash: '#E2F2ED',
  orange: '#C9652D',
  orangeWash: '#FFF0E5',
  blue: '#4C78A8',
  blueWash: '#EAF1F8',
  yellowWash: '#FFF8D9',
};

type ButtonProps = {
  label: string;
  onPress: () => void;
  variant?: 'primary' | 'secondary' | 'quiet' | 'danger';
  disabled?: boolean;
  icon?: string;
};

export function ProductButton({ label, onPress, variant = 'primary', disabled = false, icon }: ButtonProps) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityState={{ disabled }}
      disabled={disabled}
      onPress={onPress}
      style={({ pressed }) => [
        styles.button,
        variant === 'secondary' && styles.buttonSecondary,
        variant === 'quiet' && styles.buttonQuiet,
        variant === 'danger' && styles.buttonDanger,
        disabled && styles.buttonDisabled,
        pressed && !disabled && styles.buttonPressed,
      ]}
    >
      {icon ? <Text style={styles.buttonIcon}>{icon}</Text> : null}
      <Text style={[styles.buttonText, variant !== 'primary' && styles.buttonTextDark]}>{label}</Text>
    </Pressable>
  );
}

export function Card({ children, style }: { children: ReactNode; style?: object }) {
  return <View style={[styles.card, style]}>{children}</View>;
}

export function DemoBadge({ label = '데모 화면' }: { label?: string }) {
  return (
    <View style={styles.demoBadge}>
      <View style={styles.demoDot} />
      <Text style={styles.demoBadgeText}>{label}</Text>
    </View>
  );
}

export function SectionHeading({ title, action, onAction }: { title: string; action?: string; onAction?: () => void }) {
  return (
    <View style={styles.sectionHeading}>
      <Text accessibilityRole="header" style={styles.sectionTitle}>{title}</Text>
      {action && onAction ? (
        <Pressable accessibilityRole="button" onPress={onAction} hitSlop={8}>
          <Text style={styles.sectionAction}>{action}</Text>
        </Pressable>
      ) : null}
    </View>
  );
}

export function StatusPill({ label, tone = 'teal' }: { label: string; tone?: 'teal' | 'orange' | 'blue' | 'neutral' }) {
  return <View style={[styles.statusPill, styles[`statusPill${tone}`]]}><Text style={[styles.statusPillText, styles[`statusPillText${tone}`]]}>{label}</Text></View>;
}

const navItems: Array<{ tab: UserTab; label: string; icon: string }> = [
  { tab: 'home', label: '홈', icon: '⌂' },
  { tab: 'repairs', label: '수리', icon: '＋' },
  { tab: 'device', label: '내 기기', icon: '▣' },
  { tab: 'support', label: '복지지원', icon: '♡' },
  { tab: 'settings', label: '설정·알림', icon: '⋯' },
];

export function BottomNavigation({ activeTab, onChange }: { activeTab: UserTab; onChange: (tab: UserTab) => void }) {
  return (
    <View style={styles.bottomNav} accessibilityLabel="주요 메뉴">
      {navItems.map((item) => {
        const active = activeTab === item.tab;
        return (
          <Pressable
            key={item.tab}
            accessibilityRole="button"
            accessibilityState={{ selected: active }}
            accessibilityLabel={item.label}
            onPress={() => onChange(item.tab)}
            style={({ pressed }) => [styles.navItem, active && styles.navItemActive, pressed && styles.navPressed]}
          >
            <Text style={[styles.navIcon, active && styles.navIconActive]}>{item.icon}</Text>
            <Text style={[styles.navLabel, active && styles.navLabelActive]}>{item.label}</Text>
          </Pressable>
        );
      })}
    </View>
  );
}

export const styles = StyleSheet.create({
  card: { backgroundColor: colors.surface, borderColor: colors.border, borderRadius: 22, borderWidth: 1, padding: 20 },
  button: { alignItems: 'center', backgroundColor: colors.teal, borderRadius: 15, flexDirection: 'row', justifyContent: 'center', minHeight: 54, paddingHorizontal: 18 },
  buttonSecondary: { backgroundColor: colors.tealWash },
  buttonQuiet: { backgroundColor: colors.canvas },
  buttonDanger: { backgroundColor: '#A54E3E' },
  buttonDisabled: { opacity: 0.45 },
  buttonPressed: { opacity: 0.78, transform: [{ scale: 0.99 }] },
  buttonIcon: { color: '#FFFFFF', fontSize: 20, marginRight: 8 },
  buttonText: { color: '#FFFFFF', fontSize: 17, fontWeight: '800' },
  buttonTextDark: { color: colors.tealDark },
  demoBadge: { alignItems: 'center', alignSelf: 'flex-start', backgroundColor: colors.yellowWash, borderRadius: 99, flexDirection: 'row', paddingHorizontal: 10, paddingVertical: 6 },
  demoDot: { backgroundColor: '#D49A25', borderRadius: 4, height: 8, marginRight: 6, width: 8 },
  demoBadgeText: { color: '#87641B', fontSize: 12, fontWeight: '800' },
  sectionHeading: { alignItems: 'center', flexDirection: 'row', justifyContent: 'space-between', marginBottom: 12 },
  sectionTitle: { color: colors.ink, fontSize: 20, fontWeight: '800' },
  sectionAction: { color: colors.teal, fontSize: 14, fontWeight: '800' },
  statusPill: { alignSelf: 'flex-start', borderRadius: 99, paddingHorizontal: 10, paddingVertical: 6 },
  statusPillteal: { backgroundColor: colors.tealWash },
  statusPillorange: { backgroundColor: colors.orangeWash },
  statusPillblue: { backgroundColor: colors.blueWash },
  statusPillneutral: { backgroundColor: '#EEF1EF' },
  statusPillText: { fontSize: 13, fontWeight: '800' },
  statusPillTextteal: { color: colors.tealDark },
  statusPillTextorange: { color: '#9A4D22' },
  statusPillTextblue: { color: '#3E6590' },
  statusPillTextneutral: { color: colors.muted },
  bottomNav: { backgroundColor: colors.surface, borderColor: colors.border, borderTopWidth: 1, flexDirection: 'row', paddingBottom: 7, paddingHorizontal: 6, paddingTop: 8 },
  navItem: { alignItems: 'center', borderRadius: 14, flex: 1, justifyContent: 'center', minHeight: 56 },
  navItemActive: { backgroundColor: colors.tealWash },
  navPressed: { opacity: 0.7 },
  navIcon: { color: colors.faint, fontSize: 22, fontWeight: '600', lineHeight: 25 },
  navIconActive: { color: colors.tealDark },
  navLabel: { color: colors.faint, fontSize: 11, fontWeight: '700', marginTop: 2 },
  navLabelActive: { color: colors.tealDark },
});
