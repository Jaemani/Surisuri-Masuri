import DateTimePicker from '@react-native-community/datetimepicker';
import { useState } from 'react';
import { Platform, Pressable, StyleSheet, Text, View } from 'react-native';

import { formatScheduleSeoul } from '../schedule';
import { colors } from './ProductUi';

type Props = { value: Date; minimumDate: Date; maximumDate: Date; onChange: (value: Date) => void };

export function VisitDateTimePicker({ value, minimumDate, maximumDate, onChange }: Props) {
  const [mode, setMode] = useState<'date' | 'time' | null>(null);
  return <View>
    <View testID="repairer-schedule-summary" style={styles.summary}><Text style={styles.summaryLabel}>선택한 방문 일정</Text><Text accessibilityLiveRegion="polite" style={styles.summaryValue}>{formatScheduleSeoul(value)}</Text></View>
    <View style={styles.actions}>
      <Pressable accessibilityRole="button" onPress={() => setMode('date')} style={styles.button}><Text style={styles.buttonText}>날짜 선택</Text></Pressable>
      <Pressable accessibilityRole="button" onPress={() => setMode('time')} style={styles.button}><Text style={styles.buttonText}>시간 선택</Text></Pressable>
    </View>
    {mode ? <DateTimePicker
      value={value}
      mode={mode}
      display="default"
      minimumDate={mode === 'date' ? minimumDate : undefined}
      maximumDate={mode === 'date' ? maximumDate : undefined}
      is24Hour
      locale="ko-KR"
      timeZoneName="Asia/Seoul"
      onValueChange={(_event, next) => { onChange(next); if (Platform.OS === 'android') setMode(null); }}
      onDismiss={() => setMode(null)}
    /> : null}
    {Platform.OS === 'ios' && mode ? <Pressable accessibilityRole="button" onPress={() => setMode(null)} style={styles.doneButton}><Text style={styles.doneText}>선택 닫기</Text></Pressable> : null}
  </View>;
}

const styles = StyleSheet.create({
  summary:{backgroundColor:colors.canvas,borderRadius:13,marginTop:16,padding:14},summaryLabel:{color:colors.muted,fontSize:13,fontWeight:'700'},summaryValue:{color:colors.ink,fontSize:17,fontWeight:'800',marginTop:5},actions:{flexDirection:'row',gap:9,marginBottom:16,marginTop:10},button:{alignItems:'center',backgroundColor:colors.tealWash,borderColor:colors.teal,borderRadius:12,borderWidth:1,flex:1,justifyContent:'center',minHeight:48},buttonText:{color:colors.tealDark,fontSize:14,fontWeight:'800'},doneButton:{alignItems:'center',justifyContent:'center',minHeight:44},doneText:{color:colors.tealDark,fontSize:14,fontWeight:'800'},
});
