import { ActivityIndicator, Linking, Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';

import type { TripRecorderState } from '../../telemetry/useTripRecorder';
import { formatMoney, getRepairProgress, getSubsidyProgressPercent, getSubsidyRemaining, repairProgressSteps } from '../state';
import type { DeviceSummary, RepairWorkOrder, SubsidySummary } from '../types';
import { Card, colors, DemoBadge, ProductButton, SectionHeading, StatusPill } from './ProductUi';

type RecorderActions = {
  onStart: () => void;
  onResume: () => void;
  onStop: () => void;
};

type HomeProps = RecorderActions & {
  state: TripRecorderState;
  displayName: string;
  request: RepairWorkOrder | null;
  device: DeviceSummary;
  subsidy: SubsidySummary;
  onOpenRepairs: () => void;
};

function isBusy(state: TripRecorderState) {
  return state.phase === 'busy' || state.phase === 'initializing';
}

function friendlyError(state: TripRecorderState) {
  if (state.errorCode === 'location_services_disabled') return '휴대폰의 위치 서비스를 켜고 다시 시도해 주세요.';
  if (state.errorCode === 'database_unavailable') return '기록을 준비하지 못했어요. 앱을 한 번 다시 열어 주세요.';
  if (state.errorCode === 'capture_failed') return '기록이 잠시 멈췄어요. 아래 버튼을 눌러 다시 이어가 주세요.';
  return null;
}

export function UserHomeScreen({ state, displayName, request, device, subsidy, onStart, onResume, onStop, onOpenRepairs }: HomeProps) {
  const busy = isBusy(state);
  const error = friendlyError(state);
  const isRecording = state.phase === 'recording';
  const hasPausedTrip = Boolean(state.activeSession) && !isRecording;

  return (
    <ScrollView contentContainerStyle={screenStyles.content} showsVerticalScrollIndicator={false}>
      <View style={screenStyles.headerRow}>
        <View>
          <Text style={screenStyles.kicker}>오늘도 편안한 하루</Text>
          <Text accessibilityRole="header" style={screenStyles.greeting}>안녕하세요, {displayName.replace(/\s*님$/, '')}님</Text>
        </View>
        <DemoBadge />
      </View>

      {request ? <Pressable
        accessibilityLabel="진행 중인 수리 요청 보기"
        accessibilityRole="button"
        onPress={onOpenRepairs}
        style={({ pressed }) => [screenStyles.repairHero, pressed && screenStyles.pressed]}
      >
        <View style={screenStyles.repairHeroTop}>
          <View style={screenStyles.repairHeroIcon}><Text style={screenStyles.repairHeroIconText}>!</Text></View>
          <View style={screenStyles.repairHeroCopy}>
            <View style={screenStyles.repairHeroStatus}><Text style={screenStyles.cardEyebrow}>진행 중인 수리</Text><StatusPill label="수리센터 배정" tone="orange" /></View>
            <Text accessibilityRole="header" style={screenStyles.repairHeroTitle}>{request.title}</Text>
            <Text style={screenStyles.repairHeroDetail}>{request.repairer}</Text>
          </View>
        </View>
        <View style={screenStyles.nextAppointment}>
          <View><Text style={screenStyles.appointmentLabel}>다음 약속</Text><Text style={screenStyles.appointmentValue}>{request.visitAt}</Text></View>
          <Text style={screenStyles.chevron}>›</Text>
        </View>
      </Pressable> : null}

      <View style={screenStyles.homeSummaryRow}>
        <Card style={screenStyles.homeSummaryCard}>
          <Text style={screenStyles.summaryLabel}>남은 수리 지원금</Text>
          <Text style={screenStyles.summaryValue}>{formatMoney(getSubsidyRemaining(subsidy))}</Text>
          <Text style={screenStyles.summaryDetail}>{subsidy.cycle}</Text>
        </Card>
        <Card style={screenStyles.homeSummaryCard}>
          <Text style={screenStyles.summaryLabel}>내 기기 상태</Text>
          <Text style={screenStyles.summaryValue}>{device.status === 'healthy' ? '정상' : '확인 필요'}</Text>
          <Text style={screenStyles.summaryDetail}>{device.name}</Text>
        </Card>
      </View>

      <SectionHeading title="이동 사용량 기록" />
      <Card style={screenStyles.tripCard}>
        <View style={screenStyles.tripCardTop}>
          <View style={[screenStyles.tripIcon, isRecording && screenStyles.tripIconActive]}>
            <Text style={screenStyles.tripIconText}>{isRecording ? '✓' : '↗'}</Text>
          </View>
          <View style={screenStyles.tripCopy}>
            <Text style={screenStyles.cardEyebrow}>{isRecording ? '지금 기록 중' : '선택 기능'}</Text>
            <Text accessibilityRole="header" style={screenStyles.tripTitle}>
              {isRecording ? '이동 사용량을 기록하고 있어요' : hasPausedTrip ? '잠시 멈춘 기록이 있어요' : '사용량 기록하기'}
            </Text>
            <Text style={screenStyles.tripDescription}>
              {isRecording
                ? '화면을 닫아도 기록은 계속됩니다.'
                : hasPausedTrip
                  ? '기록을 이어가거나 이번 이동을 마칠 수 있어요.'
                  : '점검 시기를 더 잘 안내받고 싶을 때 사용할 수 있어요.'}
            </Text>
          </View>
        </View>

        {state.phase === 'initializing' ? (
          <View style={screenStyles.loadingRow}><ActivityIndicator color={colors.teal} /><Text style={screenStyles.loadingText}>기록을 준비하고 있어요…</Text></View>
        ) : isRecording ? (
          <View style={screenStyles.recordingFooter}>
            <View><Text style={screenStyles.recordingLabel}>현재 이동</Text><Text style={screenStyles.recordingValue}>기록 중</Text></View>
            <ProductButton label="이동 마치기" onPress={onStop} variant="danger" />
          </View>
        ) : hasPausedTrip ? (
          <View style={screenStyles.buttonStack}>
            <ProductButton disabled={busy} label={busy ? '준비 중…' : '기록 이어가기'} onPress={onResume} icon="↗" />
            <ProductButton disabled={busy} label="이번 이동 마치기" onPress={onStop} variant="quiet" />
          </View>
        ) : (
          <ProductButton disabled={busy} label={busy ? '준비 중…' : '이동 기록 시작'} onPress={onStart} icon="＋" />
        )}

        {error ? <View accessibilityRole="alert" style={screenStyles.errorBox}><Text style={screenStyles.errorText}>{error}</Text></View> : null}
      </Card>

      <Card style={screenStyles.privacyCard}>
        <Text style={screenStyles.privacyTitle}>내 기록은 안전하게 보관돼요</Text>
        <Text style={screenStyles.privacyText}>이동 기록은 먼저 휴대폰에 저장되고, 필요한 내용만 서비스에 전달돼요.</Text>
      </Card>
    </ScrollView>
  );
}

type RepairScreenProps = { request: RepairWorkOrder | null; onCreateRequest: () => void };

export function UserRepairScreen({ request, onCreateRequest }: RepairScreenProps) {
  const currentIndex = request ? getRepairProgress(request.status).currentIndex : -1;
  return (
    <ScrollView contentContainerStyle={screenStyles.content} showsVerticalScrollIndicator={false}>
      <View style={screenStyles.pageHeader}><Text style={screenStyles.pageTitle}>수리 도움</Text><Text style={screenStyles.pageSubtitle}>걱정되는 점을 남기면 가까운 수리센터와 연결해 드려요.</Text></View>
      {request ? (
        <Card style={screenStyles.requestCard}>
          <View style={screenStyles.cardHeaderRow}><View><Text style={screenStyles.cardEyebrow}>데모 요청 · {request.id}</Text><Text style={screenStyles.requestTitle}>{request.title}</Text></View><StatusPill label={request.status === 'completed' ? '완료' : '진행 중'} tone="orange" /></View>
          <Text style={screenStyles.requestMeta}>{request.createdAt} 접수 · {request.repairer}</Text>
          <View style={screenStyles.stepper}>
            {repairProgressSteps.map((step, index) => {
              const complete = index <= currentIndex;
              return (
                <View key={step.status} style={screenStyles.stepItem}>
                  <View style={[screenStyles.stepLine, index === 0 && screenStyles.stepLineHidden, complete && screenStyles.stepLineComplete]} />
                  <View style={[screenStyles.stepCircle, complete && screenStyles.stepCircleComplete]}><Text style={[screenStyles.stepNumber, complete && screenStyles.stepNumberComplete]}>{complete ? '✓' : index + 1}</Text></View>
                  <Text style={[screenStyles.stepLabel, complete && screenStyles.stepLabelComplete]}>{step.label}</Text>
                </View>
              );
            })}
          </View>
          <View style={screenStyles.visitBox}><Text style={screenStyles.visitLabel}>다음 약속</Text><Text style={screenStyles.visitValue}>{request.visitAt}</Text><Text style={screenStyles.visitDetail}>방문 전 수리센터에서 전화드릴 예정이에요.</Text></View>
          <ProductButton label="요청 내용 보기" onPress={() => undefined} variant="secondary" />
        </Card>
      ) : (
        <Card style={screenStyles.emptyCard}><View style={screenStyles.emptyIcon}><Text style={screenStyles.emptyIconText}>＋</Text></View><Text style={screenStyles.emptyTitle}>도움이 필요한 곳이 있나요?</Text><Text style={screenStyles.emptyText}>사진과 간단한 설명을 남기면 수리 상담을 시작할 수 있어요.</Text><ProductButton label="수리 요청 시작" onPress={onCreateRequest} /></Card>
      )}
      <SectionHeading title="이렇게 도와드려요" />
      <View style={screenStyles.helpGrid}><Card style={screenStyles.helpCard}><Text style={screenStyles.helpIcon}>▣</Text><Text style={screenStyles.helpTitle}>증상 남기기</Text><Text style={screenStyles.helpText}>사진 한 장으로도 충분해요.</Text></Card><Card style={screenStyles.helpCard}><Text style={screenStyles.helpIcon}>♡</Text><Text style={screenStyles.helpTitle}>센터 연결</Text><Text style={screenStyles.helpText}>가까운 센터를 찾아요.</Text></Card></View>
      <DemoBadge label="수리 요청·상태 데이터는 데모입니다" />
    </ScrollView>
  );
}

export function UserDeviceScreen({ device, onOpenRepairs }: { device: DeviceSummary; onOpenRepairs: () => void }) {
  return (
    <ScrollView contentContainerStyle={screenStyles.content} showsVerticalScrollIndicator={false}>
      <View style={screenStyles.pageHeader}><Text style={screenStyles.pageTitle}>내 기기</Text><Text style={screenStyles.pageSubtitle}>내 이동을 함께하는 기기의 기록을 한눈에 확인해요.</Text></View>
      <Card style={screenStyles.deviceCard}><View style={screenStyles.deviceVisual}><Text style={screenStyles.deviceGlyph}>♿</Text></View><View style={screenStyles.deviceDetails}><StatusPill label={device.status === 'healthy' ? '정상 작동' : '확인 필요'} tone={device.status === 'healthy' ? 'teal' : 'orange'} /><Text style={screenStyles.deviceName}>{device.name}</Text><Text style={screenStyles.deviceNumber}>등록번호 {device.registrationNumber} · {device.registeredAt}</Text></View></Card>
      <SectionHeading title="기기 타임라인" action="전체 보기" onAction={() => undefined} />
      <Card style={screenStyles.timelineCard}>{device.timeline.map((item, index) => <View key={item.id} style={screenStyles.timelineRow}><View style={screenStyles.timelineRail}><View style={[screenStyles.timelineDot, stylesByTone[item.tone]]} />{index < device.timeline.length - 1 ? <View style={screenStyles.timelineLine} /> : null}</View><View style={screenStyles.timelineCopy}><Text style={screenStyles.timelineDate}>{item.date}</Text><Text style={screenStyles.timelineTitle}>{item.title}</Text><Text style={screenStyles.timelineDetail}>{item.detail}</Text></View></View>)}</Card>
      <Card style={screenStyles.actionCard}><View><Text style={screenStyles.actionTitle}>기기 상태가 걱정되나요?</Text><Text style={screenStyles.actionText}>간단한 증상을 남기고 도움을 받아보세요.</Text></View><ProductButton label="수리 도움" onPress={onOpenRepairs} variant="secondary" /></Card>
      <DemoBadge label="기기 이력은 검토용 데모 데이터입니다" />
    </ScrollView>
  );
}

export function UserSupportScreen({ subsidy }: { subsidy: SubsidySummary }) {
  const progress = getSubsidyProgressPercent(subsidy);
  return (
    <ScrollView contentContainerStyle={screenStyles.content} showsVerticalScrollIndicator={false}>
      <View style={screenStyles.pageHeader}><Text style={screenStyles.pageTitle}>복지지원</Text><Text style={screenStyles.pageSubtitle}>받을 수 있는 지원과 사용 내역을 쉽게 확인해요.</Text></View>
      <Card style={screenStyles.subsidyCard}><View style={screenStyles.cardHeaderRow}><View><Text style={screenStyles.cardEyebrow}>{subsidy.cycle}</Text><Text style={screenStyles.subsidyTitle}>{subsidy.program}</Text></View><Text style={screenStyles.subsidyHeart}>♡</Text></View><Text style={screenStyles.subsidyAmount}>{formatMoney(getSubsidyRemaining(subsidy))} <Text style={screenStyles.subsidyAmountUnit}>남았어요</Text></Text><View style={screenStyles.progressTrack}><View style={[screenStyles.progressFill, { width: `${progress}%` }]} /></View><View style={screenStyles.progressLabels}><Text style={screenStyles.progressLabel}>사용 {formatMoney(subsidy.used)}</Text><Text style={screenStyles.progressLabel}>전체 {formatMoney(subsidy.total)}</Text></View><View style={screenStyles.supportNote}><Text style={screenStyles.supportNoteIcon}>i</Text><Text style={screenStyles.supportNoteText}>{subsidy.note}</Text></View></Card>
      <SectionHeading title="지원 안내" />
      <Card style={screenStyles.infoCard}><InfoRow icon="✓" title="수리비 지원" detail="등록된 기기의 수리비 일부를 지원해요." /><InfoRow icon="☎" title="도움이 필요할 때" detail="복지관 담당자에게 연결해 드릴게요." /><InfoRow icon="▣" title="다음 확인일" detail={subsidy.nextReview} last /></Card>
      <DemoBadge label="지원금 내역은 확인용 데모 데이터입니다" />
    </ScrollView>
  );
}

function InfoRow({ icon, title, detail, last = false }: { icon: string; title: string; detail: string; last?: boolean }) {
  return <View style={[screenStyles.infoRow, last && screenStyles.infoRowLast]}><View style={screenStyles.infoIcon}><Text style={screenStyles.infoIconText}>{icon}</Text></View><View style={screenStyles.infoCopy}><Text style={screenStyles.infoTitle}>{title}</Text><Text style={screenStyles.infoDetail}>{detail}</Text></View><Text style={screenStyles.chevron}>›</Text></View>;
}

type SettingsProps = { state: TripRecorderState; onEnableBackground: () => void; onOpenPhoneSettings: () => void; onSwitchToRepairer: () => void };

function permissionLabel(state: TripRecorderState) {
  switch (state.permission) {
    case 'granted': return '허용됨';
    case 'denied_blocked': return '휴대폰 설정에서 허용해 주세요';
    case 'denied_can_ask': return '시작할 때 다시 확인할게요';
    case 'undetermined': return '아직 확인하지 않았어요';
    default: return '확인 중';
  }
}

export function UserSettingsScreen({ state, onEnableBackground, onOpenPhoneSettings, onSwitchToRepairer }: SettingsProps) {
  const backgroundReady = state.backgroundPermission === 'granted';
  return (
    <ScrollView contentContainerStyle={screenStyles.content} showsVerticalScrollIndicator={false}>
      <View style={screenStyles.pageHeader}><Text style={screenStyles.pageTitle}>설정·알림</Text><Text style={screenStyles.pageSubtitle}>필요할 때만 바꾸면 돼요.</Text></View>
      <Card style={screenStyles.profileCard}><View style={screenStyles.avatar}><Text style={screenStyles.avatarText}>정</Text></View><View><Text style={screenStyles.profileName}>김정자 님</Text><Text style={screenStyles.profileDetail}>보호자와 함께 사용하는 계정</Text></View><Text style={screenStyles.chevron}>›</Text></Card>
      <SectionHeading title="기록 설정" />
      <Card style={screenStyles.settingsCard}><View style={screenStyles.settingRow}><View style={screenStyles.settingIcon}><Text style={screenStyles.settingIconText}>⌖</Text></View><View style={screenStyles.settingCopy}><Text style={screenStyles.settingTitle}>이동 기록</Text><Text style={screenStyles.settingDetail}>위치 권한 · {permissionLabel(state)}</Text></View><View style={[screenStyles.toggle, state.permission === 'granted' && screenStyles.toggleOn]}><View style={[screenStyles.toggleKnob, state.permission === 'granted' && screenStyles.toggleKnobOn]} /></View></View><View style={screenStyles.settingRow}><View style={screenStyles.settingIcon}><Text style={screenStyles.settingIconText}>◷</Text></View><View style={screenStyles.settingCopy}><Text style={screenStyles.settingTitle}>화면을 꺼도 기록하기</Text><Text style={screenStyles.settingDetail}>{backgroundReady ? '허용됨 · 이동 중에만 사용해요' : '이동 중에도 기록하려면 한 번 허용해 주세요'}</Text></View>{state.backgroundAvailable && !backgroundReady ? <Pressable accessibilityRole="button" onPress={onEnableBackground} style={screenStyles.smallAction}><Text style={screenStyles.smallActionText}>허용</Text></Pressable> : <Text style={screenStyles.settingCheck}>{backgroundReady ? '✓' : '—'}</Text>}</View>{state.permission === 'denied_blocked' ? <Pressable accessibilityRole="button" onPress={onOpenPhoneSettings} style={screenStyles.settingsLink}><Text style={screenStyles.settingsLinkText}>휴대폰 설정에서 위치 권한 열기</Text></Pressable> : null}</Card>
      <SectionHeading title="알림과 도움" />
      <Card style={screenStyles.settingsCard}><InfoRow icon="♢" title="알림 받기" detail="수리 일정과 지원 소식을 알려드려요." /><InfoRow icon="?" title="도움말" detail="자주 묻는 질문을 확인해요." last /></Card>
      <Pressable accessibilityRole="button" onPress={onSwitchToRepairer} style={({ pressed }) => [screenStyles.devSwitch, pressed && screenStyles.pressed]}><Text style={screenStyles.devSwitchIcon}>⚙</Text><View style={screenStyles.devSwitchCopy}><Text style={screenStyles.devSwitchTitle}>개발용 역할 전환</Text><Text style={screenStyles.devSwitchText}>수리사 화면을 확인하는 데모 기능이에요.</Text></View><Text style={screenStyles.chevron}>›</Text></Pressable>
      <DemoBadge label="김정자 님과 지원 내역은 데모 계정입니다" />
    </ScrollView>
  );
}

export function openPhoneSettings() {
  return Linking.openSettings().catch(() => undefined);
}

const screenStyles = StyleSheet.create({
  content: { backgroundColor: colors.canvas, flexGrow: 1, paddingBottom: 28, paddingHorizontal: 20, paddingTop: 24 },
  headerRow: { alignItems: 'flex-start', flexDirection: 'row', justifyContent: 'space-between', marginBottom: 24 },
  kicker: { color: colors.teal, fontSize: 14, fontWeight: '800', marginBottom: 5 },
  greeting: { color: colors.ink, fontSize: 28, fontWeight: '800', letterSpacing: -0.5 },
  repairHero: { backgroundColor: colors.surface, borderColor: colors.border, borderRadius: 22, borderWidth: 1, marginBottom: 14, padding: 19 },
  repairHeroTop: { alignItems: 'flex-start', flexDirection: 'row' },
  repairHeroIcon: { alignItems: 'center', backgroundColor: colors.orangeWash, borderRadius: 16, height: 48, justifyContent: 'center', marginRight: 13, width: 48 },
  repairHeroIconText: { color: colors.orange, fontSize: 22, fontWeight: '800' },
  repairHeroCopy: { flex: 1 },
  repairHeroStatus: { alignItems: 'center', flexDirection: 'row', justifyContent: 'space-between' },
  repairHeroTitle: { color: colors.ink, fontSize: 20, fontWeight: '800', lineHeight: 27, marginTop: 3 },
  repairHeroDetail: { color: colors.muted, fontSize: 14, marginTop: 5 },
  nextAppointment: { alignItems: 'center', backgroundColor: colors.canvas, borderRadius: 14, flexDirection: 'row', justifyContent: 'space-between', marginTop: 17, paddingHorizontal: 14, paddingVertical: 12 },
  appointmentLabel: { color: colors.muted, fontSize: 12, fontWeight: '700' },
  appointmentValue: { color: colors.ink, fontSize: 15, fontWeight: '800', marginTop: 3 },
  homeSummaryRow: { flexDirection: 'row', gap: 10, marginBottom: 25 },
  homeSummaryCard: { flex: 1, padding: 15 },
  summaryLabel: { color: colors.muted, fontSize: 12, fontWeight: '700' },
  summaryValue: { color: colors.ink, fontSize: 20, fontWeight: '800', marginTop: 6 },
  summaryDetail: { color: colors.faint, fontSize: 12, marginTop: 4 },
  tripCard: { backgroundColor: '#E8F4EF', borderColor: '#D4EAE2', marginBottom: 16 },
  tripCardTop: { flexDirection: 'row' },
  tripIcon: { alignItems: 'center', backgroundColor: '#D7E9E1', borderRadius: 18, height: 54, justifyContent: 'center', marginRight: 14, width: 54 },
  tripIconActive: { backgroundColor: '#B7E0D2' },
  tripIconText: { color: colors.tealDark, fontSize: 28, fontWeight: '700' },
  tripCopy: { flex: 1 },
  cardEyebrow: { color: colors.teal, fontSize: 13, fontWeight: '800', marginBottom: 5 },
  tripTitle: { color: colors.ink, fontSize: 20, fontWeight: '800', lineHeight: 27 },
  tripDescription: { color: colors.muted, fontSize: 14, lineHeight: 21, marginTop: 6 },
  recordingFooter: { alignItems: 'center', borderTopColor: '#D4EAE2', borderTopWidth: 1, flexDirection: 'row', justifyContent: 'space-between', marginTop: 20, paddingTop: 18 },
  recordingLabel: { color: colors.muted, fontSize: 13, fontWeight: '700' },
  recordingValue: { color: colors.tealDark, fontSize: 17, fontWeight: '800', marginTop: 2 },
  buttonStack: { gap: 10, marginTop: 20 },
  loadingRow: { alignItems: 'center', borderTopColor: '#D4EAE2', borderTopWidth: 1, flexDirection: 'row', gap: 10, marginTop: 20, paddingTop: 18 },
  loadingText: { color: colors.muted, fontSize: 15 },
  errorBox: { backgroundColor: colors.orangeWash, borderRadius: 12, marginTop: 14, padding: 12 },
  errorText: { color: '#8D4A27', fontSize: 14, lineHeight: 20 },
  newsCard: { alignItems: 'center', backgroundColor: colors.surface, borderColor: colors.border, borderRadius: 18, borderWidth: 1, flexDirection: 'row', marginBottom: 24, padding: 16 },
  newsIcon: { alignItems: 'center', backgroundColor: colors.orangeWash, borderRadius: 14, height: 42, justifyContent: 'center', marginRight: 12, width: 42 },
  newsIconText: { color: colors.orange, fontSize: 20, fontWeight: '800' },
  newsCopy: { flex: 1 },
  newsTitle: { color: colors.ink, fontSize: 15, fontWeight: '800' },
  newsDetail: { color: colors.muted, fontSize: 13, lineHeight: 19, marginTop: 4 },
  chevron: { color: colors.faint, fontSize: 28, fontWeight: '300', marginLeft: 8 },
  privacyCard: { backgroundColor: '#EEF5F1', borderColor: '#DDECE4' },
  privacyTitle: { color: colors.ink, fontSize: 15, fontWeight: '800' },
  privacyText: { color: colors.muted, fontSize: 14, lineHeight: 21, marginTop: 6 },
  pressed: { opacity: 0.72 },
  pageHeader: { marginBottom: 24 },
  pageTitle: { color: colors.ink, fontSize: 30, fontWeight: '800', letterSpacing: -0.5 },
  pageSubtitle: { color: colors.muted, fontSize: 15, lineHeight: 22, marginTop: 7 },
  cardHeaderRow: { alignItems: 'flex-start', flexDirection: 'row', justifyContent: 'space-between' },
  requestCard: { marginBottom: 28 },
  requestTitle: { color: colors.ink, fontSize: 20, fontWeight: '800', lineHeight: 27 },
  requestMeta: { color: colors.muted, fontSize: 14, lineHeight: 21, marginTop: 11 },
  stepper: { flexDirection: 'row', justifyContent: 'space-between', marginBottom: 22, marginTop: 26 },
  stepItem: { alignItems: 'center', flex: 1, position: 'relative' },
  stepLine: { backgroundColor: '#DCE5E0', height: 3, left: '-50%', position: 'absolute', right: '50%', top: 11 },
  stepLineHidden: { backgroundColor: 'transparent' },
  stepLineComplete: { backgroundColor: '#B9DDD1' },
  stepCircle: { alignItems: 'center', backgroundColor: '#EEF1EF', borderRadius: 14, height: 28, justifyContent: 'center', width: 28 },
  stepCircleComplete: { backgroundColor: colors.teal },
  stepNumber: { color: colors.faint, fontSize: 12, fontWeight: '800' },
  stepNumberComplete: { color: '#FFFFFF' },
  stepLabel: { color: colors.faint, fontSize: 11, fontWeight: '700', marginTop: 7, textAlign: 'center' },
  stepLabelComplete: { color: colors.tealDark },
  visitBox: { backgroundColor: colors.canvas, borderRadius: 15, marginBottom: 14, padding: 14 },
  visitLabel: { color: colors.muted, fontSize: 12, fontWeight: '700' },
  visitValue: { color: colors.ink, fontSize: 17, fontWeight: '800', marginTop: 4 },
  visitDetail: { color: colors.muted, fontSize: 13, lineHeight: 19, marginTop: 4 },
  emptyCard: { alignItems: 'center', marginBottom: 28, paddingVertical: 30 },
  emptyIcon: { alignItems: 'center', backgroundColor: colors.tealWash, borderRadius: 30, height: 60, justifyContent: 'center', marginBottom: 14, width: 60 },
  emptyIconText: { color: colors.teal, fontSize: 30 },
  emptyTitle: { color: colors.ink, fontSize: 20, fontWeight: '800' },
  emptyText: { color: colors.muted, fontSize: 14, lineHeight: 21, marginBottom: 18, marginTop: 7, textAlign: 'center' },
  helpGrid: { flexDirection: 'row', gap: 10, marginBottom: 18 },
  helpCard: { flex: 1, minHeight: 130, padding: 16 },
  helpIcon: { color: colors.teal, fontSize: 22, marginBottom: 10 },
  helpTitle: { color: colors.ink, fontSize: 15, fontWeight: '800' },
  helpText: { color: colors.muted, fontSize: 12, lineHeight: 18, marginTop: 4 },
  deviceCard: { alignItems: 'center', flexDirection: 'row', marginBottom: 28 },
  deviceVisual: { alignItems: 'center', backgroundColor: colors.tealWash, borderRadius: 22, height: 82, justifyContent: 'center', marginRight: 15, width: 82 },
  deviceGlyph: { color: colors.tealDark, fontSize: 39 },
  deviceDetails: { flex: 1 },
  deviceName: { color: colors.ink, fontSize: 18, fontWeight: '800', marginTop: 10 },
  deviceNumber: { color: colors.muted, fontSize: 13, lineHeight: 20, marginTop: 4 },
  timelineCard: { marginBottom: 18 },
  timelineRow: { flexDirection: 'row', minHeight: 80 },
  timelineRail: { alignItems: 'center', marginRight: 13, width: 20 },
  timelineDot: { borderRadius: 7, height: 14, marginTop: 3, width: 14 },
  timelineDotTeal: { backgroundColor: colors.teal },
  timelineDotOrange: { backgroundColor: colors.orange },
  timelineDotBlue: { backgroundColor: colors.blue },
  timelineLine: { backgroundColor: '#D9E3DD', flex: 1, marginVertical: 4, width: 2 },
  timelineCopy: { flex: 1, paddingBottom: 18 },
  timelineDate: { color: colors.faint, fontSize: 12, fontWeight: '700' },
  timelineTitle: { color: colors.ink, fontSize: 15, fontWeight: '800', marginTop: 4 },
  timelineDetail: { color: colors.muted, fontSize: 13, lineHeight: 19, marginTop: 3 },
  actionCard: { alignItems: 'center', flexDirection: 'row', justifyContent: 'space-between', marginBottom: 18, padding: 16 },
  actionTitle: { color: colors.ink, fontSize: 15, fontWeight: '800' },
  actionText: { color: colors.muted, fontSize: 12, lineHeight: 18, marginTop: 4, maxWidth: 185 },
  subsidyCard: { marginBottom: 28 },
  subsidyTitle: { color: colors.ink, fontSize: 18, fontWeight: '800', marginTop: 3 },
  subsidyHeart: { color: colors.orange, fontSize: 27 },
  subsidyAmount: { color: colors.tealDark, fontSize: 32, fontWeight: '800', marginTop: 22 },
  subsidyAmountUnit: { color: colors.muted, fontSize: 15, fontWeight: '700' },
  progressTrack: { backgroundColor: '#E8EEEA', borderRadius: 5, height: 10, marginTop: 15, overflow: 'hidden' },
  progressFill: { backgroundColor: colors.teal, borderRadius: 5, height: 10 },
  progressLabels: { flexDirection: 'row', justifyContent: 'space-between', marginTop: 7 },
  progressLabel: { color: colors.muted, fontSize: 12, fontWeight: '700' },
  supportNote: { alignItems: 'center', backgroundColor: colors.canvas, borderRadius: 12, flexDirection: 'row', marginTop: 18, padding: 11 },
  supportNoteIcon: { alignItems: 'center', borderColor: colors.teal, borderRadius: 8, borderWidth: 1, color: colors.teal, fontSize: 11, fontWeight: '800', height: 17, lineHeight: 15, marginRight: 8, textAlign: 'center', width: 17 },
  supportNoteText: { color: colors.muted, flex: 1, fontSize: 13, lineHeight: 19 },
  infoCard: { marginBottom: 18 },
  infoRow: { alignItems: 'center', borderBottomColor: colors.border, borderBottomWidth: 1, flexDirection: 'row', paddingBottom: 15, paddingTop: 2 },
  infoRowLast: { borderBottomWidth: 0, paddingBottom: 2, paddingTop: 15 },
  infoIcon: { alignItems: 'center', backgroundColor: colors.tealWash, borderRadius: 12, height: 36, justifyContent: 'center', marginRight: 11, width: 36 },
  infoIconText: { color: colors.tealDark, fontSize: 17, fontWeight: '800' },
  infoCopy: { flex: 1 },
  infoTitle: { color: colors.ink, fontSize: 15, fontWeight: '800' },
  infoDetail: { color: colors.muted, fontSize: 13, lineHeight: 19, marginTop: 3 },
  profileCard: { alignItems: 'center', flexDirection: 'row', marginBottom: 28 },
  avatar: { alignItems: 'center', backgroundColor: colors.orangeWash, borderRadius: 25, height: 50, justifyContent: 'center', marginRight: 13, width: 50 },
  avatarText: { color: colors.orange, fontSize: 22, fontWeight: '800' },
  profileName: { color: colors.ink, fontSize: 18, fontWeight: '800' },
  profileDetail: { color: colors.muted, fontSize: 13, marginTop: 4 },
  settingsCard: { marginBottom: 28, paddingVertical: 8 },
  settingRow: { alignItems: 'center', borderBottomColor: colors.border, borderBottomWidth: 1, flexDirection: 'row', minHeight: 76, paddingVertical: 10 },
  settingRowLast: { borderBottomWidth: 0 },
  settingIcon: { alignItems: 'center', backgroundColor: colors.tealWash, borderRadius: 12, height: 36, justifyContent: 'center', marginRight: 11, width: 36 },
  settingIconText: { color: colors.tealDark, fontSize: 19 },
  settingCopy: { flex: 1 },
  settingTitle: { color: colors.ink, fontSize: 15, fontWeight: '800' },
  settingDetail: { color: colors.muted, fontSize: 13, lineHeight: 19, marginTop: 3 },
  settingCheck: { color: colors.teal, fontSize: 21, fontWeight: '800', marginRight: 5 },
  toggle: { backgroundColor: '#D6DEDA', borderRadius: 15, height: 28, justifyContent: 'center', padding: 3, width: 48 },
  toggleOn: { backgroundColor: '#9CD5C5' },
  toggleKnob: { backgroundColor: '#FFFFFF', borderRadius: 11, height: 22, width: 22 },
  toggleKnobOn: { alignSelf: 'flex-end' },
  smallAction: { backgroundColor: colors.tealWash, borderRadius: 10, paddingHorizontal: 11, paddingVertical: 8 },
  smallActionText: { color: colors.tealDark, fontSize: 13, fontWeight: '800' },
  settingsLink: { borderTopColor: colors.border, borderTopWidth: 1, marginTop: 4, paddingTop: 15 },
  settingsLinkText: { color: colors.teal, fontSize: 14, fontWeight: '800' },
  devSwitch: { alignItems: 'center', backgroundColor: '#EEF1EF', borderRadius: 18, flexDirection: 'row', marginBottom: 18, padding: 15 },
  devSwitchIcon: { color: colors.muted, fontSize: 22, marginRight: 11 },
  devSwitchCopy: { flex: 1 },
  devSwitchTitle: { color: colors.ink, fontSize: 14, fontWeight: '800' },
  devSwitchText: { color: colors.muted, fontSize: 12, marginTop: 4 },
});

const stylesByTone = {
  teal: screenStyles.timelineDotTeal,
  orange: screenStyles.timelineDotOrange,
  blue: screenStyles.timelineDotBlue,
};
