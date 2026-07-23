import { StatusBar } from 'expo-status-bar';
import { useMemo } from 'react';
import {
  ActivityIndicator,
  Linking,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';

import { useTripRecorder } from './src/telemetry/useTripRecorder';

type ActionButtonProps = {
  label: string;
  onPress: () => void;
  disabled?: boolean;
  variant?: 'primary' | 'secondary' | 'danger';
};

function ActionButton({ label, onPress, disabled = false, variant = 'primary' }: ActionButtonProps) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityState={{ disabled }}
      disabled={disabled}
      onPress={onPress}
      style={({ pressed }) => [
        styles.button,
        variant === 'secondary' && styles.buttonSecondary,
        variant === 'danger' && styles.buttonDanger,
        disabled && styles.buttonDisabled,
        pressed && !disabled && styles.buttonPressed,
      ]}
    >
      <Text
        style={[
          styles.buttonText,
          variant === 'secondary' && styles.buttonSecondaryText,
        ]}
      >
        {label}
      </Text>
    </Pressable>
  );
}

function permissionCopy(permission: ReturnType<typeof useTripRecorder>['state']['permission']) {
  switch (permission) {
    case 'granted':
      return { label: '앱 사용 중 위치 허용', tone: 'positive' as const };
    case 'undetermined':
      return { label: '위치 권한 확인 전', tone: 'neutral' as const };
    case 'denied_can_ask':
      return { label: '위치 권한 요청 가능', tone: 'warning' as const };
    case 'denied_blocked':
      return { label: '설정에서 위치 권한 필요', tone: 'warning' as const };
    default:
      return { label: '권한 확인 중', tone: 'neutral' as const };
  }
}

export default function App() {
  const { state, start, resume, stop, enableBackground } = useTripRecorder();
  const permission = permissionCopy(state.permission);
  const backgroundPermission = permissionCopy(state.backgroundPermission);
  const isBusy = state.phase === 'busy' || state.phase === 'initializing';
  const startedAt = useMemo(() => {
    if (!state.activeSession) return null;
    return new Intl.DateTimeFormat('ko-KR', {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    }).format(new Date(state.activeSession.startedAt));
  }, [state.activeSession]);

  return (
    <View style={styles.screen}>
      <StatusBar style="light" />
      <ScrollView
        contentContainerStyle={styles.content}
        contentInsetAdjustmentBehavior="automatic"
      >
        <View style={styles.hero}>
          <View style={styles.badge}>
            <Text style={styles.badgeText}>
              {state.captureMode === 'background'
                ? 'BACKGROUND GPS · DEV BUILD'
                : state.captureMode === 'foreground'
                  ? 'FOREGROUND GPS · ACTIVE'
                  : 'PHONE GPS · OFFLINE FIRST'}
            </Text>
          </View>
          <Text accessibilityRole="header" style={styles.title}>
            오늘의 이동을{`\n`}안전하게 기록해요
          </Text>
          <Text style={styles.introduction}>
            주행 시작을 누른 동안 휴대폰 위치를 먼저 기기 안에 저장합니다.
            백그라운드 권한은 사용자가 따로 허용한 경우에만 사용합니다.
          </Text>
        </View>

        <View style={styles.panel}>
          <View style={styles.panelHeader}>
            <View>
              <Text style={styles.panelEyebrow}>수집 상태</Text>
              <Text accessibilityRole="header" style={styles.panelTitle}>
                {state.phase === 'recording'
                  ? '주행 기록 중'
                  : state.activeSession
                    ? '중단된 기록 발견'
                    : '기록 대기'}
              </Text>
            </View>
            <View
              accessibilityLabel={state.phase === 'recording' ? '수집 중' : '수집하지 않음'}
              style={[
                styles.liveIndicator,
                state.phase === 'recording' && styles.liveIndicatorActive,
              ]}
            />
          </View>

          {state.phase === 'initializing' ? (
            <View style={styles.loadingRow}>
              <ActivityIndicator color="#1D6B58" />
              <Text style={styles.loadingText}>기기 저장소를 확인하고 있습니다.</Text>
            </View>
          ) : (
            <>
              <View style={styles.metricGrid}>
                <View style={styles.metric}>
                  <Text style={styles.metricValue}>
                    {state.activeSession?.acceptedSampleCount ?? 0}
                  </Text>
                  <Text style={styles.metricLabel}>저장된 위치</Text>
                </View>
                <View style={styles.metric}>
                  <Text style={styles.metricValue}>{state.pendingUploadCount}</Text>
                  <Text style={styles.metricLabel}>서버 전송 대기</Text>
                </View>
              </View>

              {startedAt ? (
                <Text style={styles.sessionNote}>
                  {startedAt} 시작 · 품질 기준 제외 {state.activeSession?.rejectedSampleCount ?? 0}건
                </Text>
              ) : null}

              <View style={styles.divider} />

              <View style={styles.statusRow}>
                <View
                  style={[
                    styles.statusDot,
                    permission.tone === 'positive' && styles.statusDotPositive,
                    permission.tone === 'warning' && styles.statusDotWarning,
                  ]}
                />
                <View style={styles.statusCopy}>
                  <Text style={styles.statusLabel}>위치 권한</Text>
                  <Text style={styles.statusDetail}>{permission.label}</Text>
                </View>
              </View>

              <View style={styles.statusRowSpaced}>
                <View
                  style={[
                    styles.statusDot,
                    state.backgroundPermission === 'granted' && styles.statusDotPositive,
                    state.backgroundAvailable &&
                      state.backgroundPermission !== 'granted' &&
                      styles.statusDotWarning,
                  ]}
                />
                <View style={styles.statusCopy}>
                  <Text style={styles.statusLabel}>화면 밖 기록</Text>
                  <Text style={styles.statusDetail}>
                    {!state.backgroundAvailable
                      ? 'Android/iPhone development build에서 확인할 수 있어요.'
                      : state.backgroundPermission === 'granted'
                        ? state.captureMode === 'background'
                          ? '백그라운드 수집을 요청했어요. 실기기 지속 검증 전입니다.'
                          : '필요한 권한이 준비되었어요.'
                        : backgroundPermission.label}
                  </Text>
                </View>
              </View>

              {state.errorCode ? (
                <View accessibilityRole="alert" style={styles.alert}>
                  <Text style={styles.alertTitle}>확인이 필요합니다</Text>
                  <Text style={styles.alertText}>
                    {state.errorCode === 'location_services_disabled'
                      ? '휴대폰의 위치 서비스를 켠 뒤 다시 시도해 주세요.'
                      : state.errorCode === 'database_unavailable'
                        ? '기기 저장소를 열 수 없습니다. 앱을 다시 시작해 주세요.'
                        : '위치 기록을 이어갈 수 없습니다. 저장 상태를 확인해 주세요.'}
                  </Text>
                </View>
              ) : null}

              <View style={styles.actions}>
                {!state.activeSession ? (
                  <ActionButton
                    disabled={isBusy}
                    label={isBusy ? '준비 중…' : '주행 시작'}
                    onPress={() => void start()}
                  />
                ) : state.phase === 'recording' ? (
                  <ActionButton
                    label="주행 종료"
                    onPress={() => void stop()}
                    variant="danger"
                  />
                ) : (
                  <>
                    <ActionButton
                      disabled={isBusy}
                      label={isBusy ? '준비 중…' : '기록 재개'}
                      onPress={() => void resume()}
                    />
                    <ActionButton
                      disabled={isBusy}
                      label="이 주행 종료"
                      onPress={() => void stop()}
                      variant="secondary"
                    />
                  </>
                )}

                {state.permission === 'denied_blocked' ? (
                  <ActionButton
                    label="휴대폰 설정 열기"
                    onPress={() => void Linking.openSettings().catch(() => undefined)}
                    variant="secondary"
                  />
                ) : null}

                {state.backgroundAvailable &&
                state.backgroundPermission !== 'granted' ? (
                  <ActionButton
                    disabled={isBusy}
                    label="화면 밖에서도 기록 허용"
                    onPress={() => void enableBackground()}
                    variant="secondary"
                  />
                ) : null}

                {state.backgroundAvailable &&
                state.backgroundPermission === 'granted' &&
                state.activeSession &&
                state.captureMode !== 'background' ? (
                  <ActionButton
                    disabled={isBusy}
                    label="백그라운드 기록으로 전환"
                    onPress={() => void enableBackground()}
                    variant="secondary"
                  />
                ) : null}
              </View>
            </>
          )}
        </View>

        <View style={styles.privacyCard}>
          <Text style={styles.privacyTitle}>지금 지키는 원칙</Text>
          <Text style={styles.privacyText}>
            원본 좌표는 이 화면이나 개발 로그에 표시하지 않습니다. 서버 동기화는 아직 켜지지
            않았으며, 이벤트는 SQLite에 서버 전송 대상이 아닌 개발 데이터로 남습니다.
            Expo Go에서는 화면 안 기록만 사용할 수 있습니다. 백그라운드 기록 코드는
            development build용이며 Android/iPhone 실기기 수명주기 검증 전입니다.
          </Text>
        </View>
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  screen: { flex: 1, backgroundColor: '#0F2531' },
  content: { flexGrow: 1, paddingBottom: 40 },
  hero: { paddingHorizontal: 24, paddingBottom: 30, paddingTop: 72 },
  badge: {
    alignSelf: 'flex-start',
    borderColor: '#4A746F',
    borderRadius: 999,
    borderWidth: 1,
    paddingHorizontal: 12,
    paddingVertical: 7,
  },
  badgeText: { color: '#A9D6CA', fontSize: 12, fontWeight: '800', letterSpacing: 0.8 },
  title: {
    color: '#F6F7F2',
    fontSize: 36,
    fontWeight: '800',
    letterSpacing: -1.1,
    lineHeight: 46,
    marginTop: 20,
  },
  introduction: { color: '#BED0D3', fontSize: 17, lineHeight: 27, marginTop: 14 },
  panel: {
    backgroundColor: '#F7F6F1',
    borderRadius: 28,
    marginHorizontal: 16,
    padding: 24,
  },
  panelHeader: { alignItems: 'center', flexDirection: 'row', justifyContent: 'space-between' },
  panelEyebrow: { color: '#56706D', fontSize: 13, fontWeight: '800' },
  panelTitle: { color: '#142B34', fontSize: 25, fontWeight: '800', marginTop: 6 },
  liveIndicator: { backgroundColor: '#A9B4B2', borderRadius: 10, height: 20, width: 20 },
  liveIndicatorActive: { backgroundColor: '#2D9B78' },
  loadingRow: { alignItems: 'center', flexDirection: 'row', gap: 12, minHeight: 120 },
  loadingText: { color: '#56676C', flex: 1, fontSize: 16, lineHeight: 24 },
  metricGrid: { flexDirection: 'row', gap: 12, marginTop: 26 },
  metric: { backgroundColor: '#E9EEE9', borderRadius: 18, flex: 1, padding: 16 },
  metricValue: { color: '#173B34', fontSize: 28, fontWeight: '800' },
  metricLabel: { color: '#5D6C69', fontSize: 13, lineHeight: 19, marginTop: 4 },
  sessionNote: { color: '#66736F', fontSize: 13, lineHeight: 20, marginTop: 12 },
  divider: { backgroundColor: '#D9DEDA', height: 1, marginVertical: 22 },
  statusRow: { alignItems: 'flex-start', flexDirection: 'row', gap: 12 },
  statusRowSpaced: {
    alignItems: 'flex-start',
    flexDirection: 'row',
    gap: 12,
    marginTop: 16,
  },
  statusDot: { backgroundColor: '#9AA5A3', borderRadius: 6, height: 12, marginTop: 5, width: 12 },
  statusDotPositive: { backgroundColor: '#2D8A69' },
  statusDotWarning: { backgroundColor: '#BD762C' },
  statusCopy: { flex: 1 },
  statusLabel: { color: '#253A40', fontSize: 15, fontWeight: '700' },
  statusDetail: { color: '#667378', fontSize: 14, lineHeight: 21, marginTop: 3 },
  alert: { backgroundColor: '#F6E7D6', borderRadius: 16, marginTop: 18, padding: 16 },
  alertTitle: { color: '#75451D', fontSize: 15, fontWeight: '800' },
  alertText: { color: '#79583A', fontSize: 14, lineHeight: 21, marginTop: 4 },
  actions: { gap: 10, marginTop: 24 },
  button: {
    alignItems: 'center',
    backgroundColor: '#1B6956',
    borderRadius: 16,
    justifyContent: 'center',
    minHeight: 54,
    paddingHorizontal: 18,
  },
  buttonSecondary: { backgroundColor: '#E2E9E5' },
  buttonDanger: { backgroundColor: '#963E35' },
  buttonDisabled: { opacity: 0.48 },
  buttonPressed: { opacity: 0.82, transform: [{ scale: 0.99 }] },
  buttonText: { color: '#FFFFFF', fontSize: 17, fontWeight: '800' },
  buttonSecondaryText: { color: '#24473E' },
  privacyCard: {
    borderColor: '#35505A',
    borderRadius: 20,
    borderWidth: 1,
    marginHorizontal: 16,
    marginTop: 16,
    padding: 20,
  },
  privacyTitle: { color: '#D8E4E1', fontSize: 15, fontWeight: '800' },
  privacyText: { color: '#A9BDC1', fontSize: 14, lineHeight: 22, marginTop: 8 },
});
