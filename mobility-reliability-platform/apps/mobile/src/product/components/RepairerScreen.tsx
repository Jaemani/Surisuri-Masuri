import { Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';

import type { RepairJob } from '../types';
import { Card, colors, DemoBadge, ProductButton, SectionHeading, StatusPill } from './ProductUi';

type RepairerScreenProps = { jobs: RepairJob[]; displayName: string; onSwitchToUser: () => void };

export function RepairerScreen({ jobs, displayName, onSwitchToUser }: RepairerScreenProps) {
  return (
    <View style={styles.screen}>
      <ScrollView contentContainerStyle={styles.content} showsVerticalScrollIndicator={false}>
        <View style={styles.topBar}>
          <View><Text style={styles.kicker}>{displayName}</Text><Text accessibilityRole="header" style={styles.title}>오늘의 작업</Text></View>
          <Pressable accessibilityRole="button" accessibilityLabel="사용자 화면으로 전환" onPress={onSwitchToUser} style={styles.roleButton}><Text style={styles.roleButtonText}>사용자 화면</Text></Pressable>
        </View>
        <DemoBadge label="수리사 역할 · 데모 데이터" />

        <Card style={styles.scanCard}>
          <View style={styles.scanIcon}><Text style={styles.scanGlyph}>⌗</Text></View>
          <View style={styles.scanCopy}><Text style={styles.scanTitle}>기기 QR로 빠르게 확인</Text><Text style={styles.scanText}>고객의 기기 등록번호를 스캔하면 수리 이력과 요청을 확인할 수 있어요.</Text></View>
          <ProductButton label="QR 확인" onPress={() => undefined} variant="secondary" icon="▣" />
        </Card>

        <SectionHeading title="오늘 처리할 작업" action={`전체 ${jobs.length}건`} onAction={() => undefined} />
        <View style={styles.jobList}>{jobs.map((job) => <RepairJobCard key={job.id} {...job} />)}</View>

        <Card style={styles.summaryCard}><View style={styles.summaryIcon}><Text style={styles.summaryGlyph}>✓</Text></View><View style={styles.summaryCopy}><Text style={styles.summaryTitle}>이번 주 점검 8건 완료</Text><Text style={styles.summaryText}>고객의 다음 방문을 미리 준비해 보세요.</Text></View><Text style={styles.chevron}>›</Text></Card>
        <DemoBadge label="고객·작업·기기 정보는 검토용 데모입니다" />
      </ScrollView>
    </View>
  );
}

function RepairJobCard({ id, customer, device, issue, due, priority }: RepairJob) {
  return (
    <Card style={styles.jobCard}>
      <View style={styles.jobTop}><StatusPill label={priority === 'today' ? '오늘 방문' : '예정'} tone={priority === 'today' ? 'orange' : 'blue'} /><Text style={styles.jobDue}>{due}</Text></View>
      <Text style={styles.customer}>{customer}</Text><Text style={styles.device}>{device}</Text>
      <View style={styles.issueRow}><View style={styles.issueDot} /><Text style={styles.issue}>{issue}</Text></View>
      <View style={styles.jobFooter}><Text style={styles.jobId}>데모 작업 · {id}</Text><Pressable accessibilityRole="button" onPress={() => undefined}><Text style={styles.detailAction}>상세 보기 ›</Text></Pressable></View>
    </Card>
  );
}

const styles = StyleSheet.create({
  screen: { backgroundColor: colors.canvas, flex: 1 },
  content: { paddingBottom: 32, paddingHorizontal: 20, paddingTop: 25 },
  topBar: { alignItems: 'flex-start', flexDirection: 'row', justifyContent: 'space-between', marginBottom: 14 },
  kicker: { color: colors.orange, fontSize: 14, fontWeight: '800', marginBottom: 5 },
  title: { color: colors.ink, fontSize: 31, fontWeight: '800', letterSpacing: -0.5 },
  roleButton: { backgroundColor: colors.surface, borderColor: colors.border, borderRadius: 12, borderWidth: 1, marginTop: 8, paddingHorizontal: 11, paddingVertical: 9 },
  roleButtonText: { color: colors.tealDark, fontSize: 12, fontWeight: '800' },
  scanCard: { alignItems: 'center', backgroundColor: colors.ink, borderColor: colors.ink, flexDirection: 'row', marginBottom: 28, marginTop: 18, padding: 17 },
  scanIcon: { alignItems: 'center', backgroundColor: '#3A555A', borderRadius: 17, height: 52, justifyContent: 'center', marginRight: 12, width: 52 },
  scanGlyph: { color: '#D7ECE5', fontSize: 31 },
  scanCopy: { flex: 1, marginRight: 10 },
  scanTitle: { color: '#FFFFFF', fontSize: 16, fontWeight: '800' },
  scanText: { color: '#B7C8C8', fontSize: 12, lineHeight: 18, marginTop: 4 },
  jobList: { gap: 10, marginBottom: 20 },
  jobCard: { padding: 17 },
  jobTop: { alignItems: 'center', flexDirection: 'row', justifyContent: 'space-between' },
  jobDue: { color: colors.muted, fontSize: 12, fontWeight: '700' },
  customer: { color: colors.ink, fontSize: 19, fontWeight: '800', marginTop: 13 },
  device: { color: colors.muted, fontSize: 13, marginTop: 4 },
  issueRow: { alignItems: 'center', flexDirection: 'row', marginTop: 13 },
  issueDot: { backgroundColor: colors.orange, borderRadius: 4, height: 8, marginRight: 7, width: 8 },
  issue: { color: colors.ink, fontSize: 14, fontWeight: '700' },
  jobFooter: { alignItems: 'center', borderTopColor: colors.border, borderTopWidth: 1, flexDirection: 'row', justifyContent: 'space-between', marginTop: 15, paddingTop: 13 },
  jobId: { color: colors.faint, fontSize: 11, fontWeight: '700' },
  detailAction: { color: colors.teal, fontSize: 13, fontWeight: '800' },
  summaryCard: { alignItems: 'center', flexDirection: 'row', marginBottom: 18, padding: 16 },
  summaryIcon: { alignItems: 'center', backgroundColor: colors.tealWash, borderRadius: 15, height: 43, justifyContent: 'center', marginRight: 11, width: 43 },
  summaryGlyph: { color: colors.teal, fontSize: 21, fontWeight: '800' },
  summaryCopy: { flex: 1 },
  summaryTitle: { color: colors.ink, fontSize: 14, fontWeight: '800' },
  summaryText: { color: colors.muted, fontSize: 12, marginTop: 4 },
  chevron: { color: colors.faint, fontSize: 28, fontWeight: '300' },
});
