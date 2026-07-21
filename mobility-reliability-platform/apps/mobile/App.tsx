import { StatusBar } from 'expo-status-bar';
import { ScrollView, StyleSheet, Text, View } from 'react-native';

type StatusItemProps = {
  title: string;
  detail: string;
  tone: 'ready' | 'pending';
};

function StatusItem({ title, detail, tone }: StatusItemProps) {
  const isReady = tone === 'ready';

  return (
    <View
      accessible
      accessibilityLabel={`${title}. ${detail}`}
      style={styles.statusItem}
    >
      <View
        style={[
          styles.statusIndicator,
          isReady ? styles.statusIndicatorReady : styles.statusIndicatorPending,
        ]}
      />
      <View style={styles.statusCopy}>
        <Text style={styles.statusTitle}>{title}</Text>
        <Text style={styles.statusDetail}>{detail}</Text>
      </View>
    </View>
  );
}

export default function App() {
  return (
    <View style={styles.screen}>
      <StatusBar style="dark" />
      <ScrollView
        contentContainerStyle={styles.content}
        contentInsetAdjustmentBehavior="automatic"
      >
        <View style={styles.badge}>
          <Text style={styles.badgeText}>개발 환경</Text>
        </View>

        <Text accessibilityRole="header" style={styles.title}>
          Mobility Reliability Dev
        </Text>
        <Text style={styles.introduction}>
          전동보장구 신뢰성 관리 플랫폼의 Android·iOS 공통 기반을 준비하고 있습니다.
        </Text>

        <View
          accessibilityLabel="현재 개발 상태"
          accessibilityRole="summary"
          style={styles.panel}
        >
          <Text style={styles.panelEyebrow}>현재 단계</Text>
          <Text accessibilityRole="header" style={styles.panelTitle}>
            신규 모바일 기반 준비
          </Text>
          <Text style={styles.panelDescription}>
            이번 단계는 새 앱의 실행 환경과 다음 vertical slice의 의존성만 마련합니다.
          </Text>

          <View style={styles.divider} />

          <StatusItem
            title="GPS 수집 미착수"
            detail="위치 권한 요청과 좌표 수집은 아직 구현하지 않았습니다."
            tone="pending"
          />
          <StatusItem
            title="오프라인 동기화 미착수"
            detail="SQLite outbox와 재연결 전송은 다음 단계에서 검증합니다."
            tone="pending"
          />
        </View>

        <Text style={styles.note}>
          실제 기능 상태와 검증 결과는 구현 이후 별도 증빙과 함께 갱신합니다.
        </Text>
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  screen: {
    flex: 1,
    backgroundColor: '#F3F6FA',
  },
  content: {
    flexGrow: 1,
    justifyContent: 'center',
    paddingHorizontal: 24,
    paddingVertical: 56,
  },
  badge: {
    alignSelf: 'flex-start',
    borderRadius: 999,
    backgroundColor: '#DDE7F5',
    paddingHorizontal: 12,
    paddingVertical: 7,
  },
  badgeText: {
    color: '#23476F',
    fontSize: 14,
    fontWeight: '700',
  },
  title: {
    marginTop: 18,
    color: '#17243A',
    fontSize: 32,
    fontWeight: '800',
    letterSpacing: -0.8,
    lineHeight: 40,
  },
  introduction: {
    marginTop: 12,
    color: '#526178',
    fontSize: 18,
    lineHeight: 28,
  },
  panel: {
    marginTop: 32,
    borderWidth: 1,
    borderColor: '#DCE3ED',
    borderRadius: 24,
    backgroundColor: '#FFFFFF',
    padding: 24,
    shadowColor: '#17243A',
    shadowOffset: { width: 0, height: 8 },
    shadowOpacity: 0.08,
    shadowRadius: 24,
    elevation: 3,
  },
  panelEyebrow: {
    color: '#31644F',
    fontSize: 14,
    fontWeight: '800',
  },
  panelTitle: {
    marginTop: 8,
    color: '#17243A',
    fontSize: 24,
    fontWeight: '800',
    lineHeight: 32,
  },
  panelDescription: {
    marginTop: 10,
    color: '#5B687B',
    fontSize: 16,
    lineHeight: 25,
  },
  divider: {
    height: 1,
    marginVertical: 22,
    backgroundColor: '#E7EBF1',
  },
  statusItem: {
    minHeight: 72,
    flexDirection: 'row',
    alignItems: 'flex-start',
    gap: 14,
    paddingVertical: 10,
  },
  statusIndicator: {
    width: 12,
    height: 12,
    marginTop: 6,
    borderRadius: 6,
  },
  statusIndicatorReady: {
    backgroundColor: '#2D7B60',
  },
  statusIndicatorPending: {
    backgroundColor: '#B36B1E',
  },
  statusCopy: {
    flex: 1,
  },
  statusTitle: {
    color: '#26354B',
    fontSize: 17,
    fontWeight: '700',
    lineHeight: 24,
  },
  statusDetail: {
    marginTop: 4,
    color: '#667489',
    fontSize: 15,
    lineHeight: 23,
  },
  note: {
    marginTop: 20,
    color: '#667489',
    fontSize: 14,
    lineHeight: 22,
  },
});
